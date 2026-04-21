package sandbox

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"sync"
	"syscall"
)

// This file centralizes the "how do we restrict an untrusted child process?"
// logic used by every exec path in this package.
//
// Layers of defense applied to every sandboxed run, in order:
//
//  1. kernel rlimits via prlimit(1) — enforced by setrlimit() before execve().
//     This is the primary guard: if the binary opens too many FDs, forks too
//     many processes, allocates too much memory, or runs too long on-CPU,
//     the kernel kills or EAGAINs it. Not dependent on shell cooperation.
//
//  2. belt-and-suspenders "ulimit" wrapper inside /bin/sh -c (kept so even if
//     prlimit is unavailable for any reason we still have _something_).
//
//  3. Go context deadline (wall-clock timeout with a small buffer above the
//     CPU budget; catches sleeps, I/O waits, and runaway non-CPU-bound code).
//
//  4. process-group kill on timeout so children of the child don't linger.
//
//  5. UID drop to a dedicated "sandbox" user when the parent process is root
//     (production container). Skipped gracefully on dev machines so engineers
//     can still test the sandbox locally.
//
//  6. process-wide concurrency semaphore so N parallel submissions cannot
//     multiply the per-submission memory budget beyond what the host can
//     take; excess submissions queue briefly.
//
// Higher-level isolation (namespaces, seccomp, chroot, cgroup-v2 direct
// manipulation) is intentionally NOT done here — that belongs in a follow-up
// that likely introduces isolate(1) or nsjail(1) as a dedicated worker.

// ────────────────────────────────────────────────────────────────────────
//  prlimit detection
// ────────────────────────────────────────────────────────────────────────

// prlimitPath is the absolute path to prlimit(1), or "" if it isn't on $PATH.
// Resolved exactly once at startup so every exec doesn't re-stat.
var prlimitPath string

// prlimitWarnOnce ensures we don't flood logs when prlimit is missing.
var prlimitWarnOnce sync.Once

func init() {
	// prlimit ships in util-linux and is available on every mainstream Linux
	// distro. It's absent on macOS / BSD. We don't panic if it's missing —
	// the sandbox falls back to ulimit + ctx deadline and logs a warning.
	if p, err := exec.LookPath("prlimit"); err == nil {
		prlimitPath = p
	} else if runtime.GOOS == "linux" {
		// On Linux we really do want prlimit; warn once so ops notices.
		prlimitWarnOnce.Do(func() {
			log.Printf("sandbox: prlimit(1) not found on PATH; " +
				"falling back to shell ulimit which is less reliable. " +
				"Install util-linux (Alpine: `apk add util-linux`) for robust limits.")
		})
	}
}

// ────────────────────────────────────────────────────────────────────────
//  prlimit argv construction
// ────────────────────────────────────────────────────────────────────────

// limitSpec describes a set of rlimits for one child process.
//
// Zero values mean "don't set this limit". Callers should prefer the
// helper constructors (execLimits, compileLimits) to get sensible defaults.
type limitSpec struct {
	addressSpaceBytes uint64 // RLIMIT_AS  — virtual memory
	cpuSeconds        uint64 // RLIMIT_CPU — CPU time
	fileSizeBytes     uint64 // RLIMIT_FSIZE — max size of any file written
	stackBytes        uint64 // RLIMIT_STACK — stack
	maxProcesses      uint64 // RLIMIT_NPROC — forks/threads (per-user)
	maxOpenFiles      uint64 // RLIMIT_NOFILE — open FDs
	coreBytes         uint64 // RLIMIT_CORE — core dump (0 disables)
	coreZero          bool   // force RLIMIT_CORE = 0 even if coreBytes is 0
}

// execLimits returns the limit set used for running a user-submitted binary.
func execLimits(memoryMB, cpuSeconds int) limitSpec {
	return limitSpec{
		addressSpaceBytes: uint64(memoryMB) * 1024 * 1024,
		cpuSeconds:        uint64(cpuSeconds),
		fileSizeBytes:     10 * 1024 * 1024, // 10 MB written per file
		stackBytes:        64 * 1024 * 1024, // 64 MB stack
		maxProcesses:      64,               // fork-bomb guard (shared across sandbox uid)
		maxOpenFiles:      64,
		coreZero:          true,
	}
}

// compileLimits returns a looser limit set appropriate for running g++.
// g++ legitimately allocates 300–800 MB on heavy templates, so the memory
// cap has to be higher than execLimits — but it still has to exist.
//
// NOTE: no maxProcesses cap on compilation — gcc / clang legitimately fork
// helper processes (cc1plus, ld, as, …) and a low nproc limit will abort
// the compile with "Resource temporarily unavailable". Concurrency of
// compiler processes is already bounded at the process level by the
// sandboxSlots semaphore.
func compileLimits(cpuSeconds int) limitSpec {
	return limitSpec{
		addressSpaceBytes: 1536 * 1024 * 1024, // 1.5 GB — kills meta-program bombs
		cpuSeconds:        uint64(cpuSeconds),
		fileSizeBytes:     32 * 1024 * 1024, // 32 MB per output file
		stackBytes:        64 * 1024 * 1024,
		maxOpenFiles:      256, // g++ opens many headers
		coreZero:          true,
	}
}

// prlimitWrap returns the prlimit(1) command prefix for `spec`, or nil if
// prlimit isn't available on this host. When nil is returned callers should
// fall back to the existing ulimit-in-shell path.
//
// Usage: append the actual argv of the target program to the returned slice.
func prlimitWrap(spec limitSpec) []string {
	if prlimitPath == "" {
		return nil
	}
	args := []string{prlimitPath}
	if spec.addressSpaceBytes > 0 {
		args = append(args, "--as="+strconv.FormatUint(spec.addressSpaceBytes, 10))
	}
	if spec.cpuSeconds > 0 {
		args = append(args, "--cpu="+strconv.FormatUint(spec.cpuSeconds, 10))
	}
	if spec.fileSizeBytes > 0 {
		// --fsize takes blocks of 1024 bytes by default in some versions; use
		// bytes form explicitly. util-linux prlimit accepts plain bytes for
		// --fsize since ~2.28, which is everywhere we'd deploy.
		args = append(args, "--fsize="+strconv.FormatUint(spec.fileSizeBytes, 10))
	}
	if spec.stackBytes > 0 {
		args = append(args, "--stack="+strconv.FormatUint(spec.stackBytes, 10))
	}
	if spec.maxProcesses > 0 {
		args = append(args, "--nproc="+strconv.FormatUint(spec.maxProcesses, 10))
	}
	if spec.maxOpenFiles > 0 {
		args = append(args, "--nofile="+strconv.FormatUint(spec.maxOpenFiles, 10))
	}
	if spec.coreZero {
		args = append(args, "--core=0")
	} else if spec.coreBytes > 0 {
		args = append(args, "--core="+strconv.FormatUint(spec.coreBytes, 10))
	}
	// Separator so prlimit doesn't try to parse the wrapped program's flags.
	args = append(args, "--")
	return args
}

// ulimitFallback returns a shell fragment equivalent to the prlimit set.
// Used only when prlimit(1) is unavailable (macOS dev, stripped containers).
//
// Each ulimit is its own statement with ||true so a rejected flag (e.g.
// macOS Bash rejects `-v`) doesn't abort the whole shell and leave the
// binary running with no limits at all. The upshot: on macOS some limits
// won't apply — use prlimit for production.
func ulimitFallback(spec limitSpec) string {
	var frag string
	appendLimit := func(cmd string) {
		// "|| true" keeps the shell exit code 0 so the next semicolon-sep
		// statement still runs even if this specific limit can't be set.
		frag += cmd + " 2>/dev/null || true; "
	}
	if spec.addressSpaceBytes > 0 {
		appendLimit(fmt.Sprintf("ulimit -v %d", spec.addressSpaceBytes/1024))
	}
	if spec.cpuSeconds > 0 {
		appendLimit(fmt.Sprintf("ulimit -t %d", spec.cpuSeconds))
	}
	if spec.fileSizeBytes > 0 {
		appendLimit(fmt.Sprintf("ulimit -f %d", spec.fileSizeBytes/1024))
	}
	if spec.maxProcesses > 0 {
		appendLimit(fmt.Sprintf("ulimit -u %d", spec.maxProcesses))
	}
	if spec.maxOpenFiles > 0 {
		appendLimit(fmt.Sprintf("ulimit -n %d", spec.maxOpenFiles))
	}
	if spec.coreZero {
		appendLimit("ulimit -c 0")
	}
	return frag
}

// ────────────────────────────────────────────────────────────────────────
//  UID drop
// ────────────────────────────────────────────────────────────────────────

// sandboxCredential resolves once at startup what Credential (if any) we can
// safely apply to child processes.
//
//   - If we're root AND a "sandbox" user exists, drop to it.
//   - If we're root AND uid 1001 exists (pre-existing convention), drop to it.
//   - Otherwise (dev on macOS, unprivileged container) return nil and let the
//     child run as the parent's uid. The rest of the sandbox still applies.
var (
	sandboxCredOnce sync.Once
	sandboxCred     *syscall.Credential
)

func getSandboxCredential() *syscall.Credential {
	sandboxCredOnce.Do(func() {
		euid := syscall.Geteuid()
		if euid != 0 {
			// Not root: setuid() will fail. Don't even try.
			if runtime.GOOS == "linux" {
				log.Printf("sandbox: running as uid %d (not root); child processes will NOT be uid-dropped. "+
					"This is safe only if the parent itself is already a constrained user.", euid)
			}
			return
		}
		// Prefer a named "sandbox" user; fall back to 1001 for compatibility
		// with the existing Dockerfile.
		if u, err := user.Lookup("sandbox"); err == nil {
			uid, _ := strconv.ParseUint(u.Uid, 10, 32)
			gid, _ := strconv.ParseUint(u.Gid, 10, 32)
			sandboxCred = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
			return
		}
		// Is uid 1001 actually a valid account on this system?
		if _, err := user.LookupId("1001"); err == nil {
			sandboxCred = &syscall.Credential{Uid: 1001, Gid: 1001}
			return
		}
		log.Printf("sandbox: running as root but no 'sandbox' / uid-1001 user found; " +
			"child processes will run as root. Create a sandbox user for safe isolation.")
	})
	return sandboxCred
}

// ────────────────────────────────────────────────────────────────────────
//  Concurrency semaphore
// ────────────────────────────────────────────────────────────────────────

// sandboxSlots caps the number of concurrent sandbox executions process-wide.
//
// Per-submission memory budgets are enforced per-process, but they only
// protect the host if N submissions × budget ≤ host RAM. A small bounded
// semaphore keeps N from growing without bound under load; excess requests
// queue on Acquire() for at most a few seconds in practice.
//
// We size at NumCPU because CPU is the true bottleneck for compile+exec, and
// each slot is already allowed ~256 MB. Override with SANDBOX_SLOTS for ops.
var sandboxSlots chan struct{}

func init() {
	n := runtime.NumCPU()
	if v := os.Getenv("SANDBOX_SLOTS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	if n < 1 {
		n = 1
	}
	sandboxSlots = make(chan struct{}, n)
}

// acquireSlot blocks until a sandbox slot is free; returns a release func.
// The release func MUST be called (typically via defer).
func acquireSlot() func() {
	sandboxSlots <- struct{}{}
	return func() { <-sandboxSlots }
}
