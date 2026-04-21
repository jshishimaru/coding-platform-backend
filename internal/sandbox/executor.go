package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Config holds sandbox resource limits.
type Config struct {
	MaxTimeSec     int // Max wall-clock time for execution (default 10)
	MaxMemoryMB    int // Max memory in MB (default 256)
	MaxOutputBytes int // Max output size in bytes (default 1MB)
}

// Result holds the outcome of a sandbox execution.
type Result struct {
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	ExitCode      int    `json:"exit_code"`
	TimeTakenMs   int64  `json:"time_taken_ms"`
	MemoryUsedKB  int64  `json:"memory_used_kb"`
	Status        string `json:"status"`
	CompileTimeMs int64  `json:"compile_time_ms"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		MaxTimeSec:     10,
		MaxMemoryMB:    256,
		MaxOutputBytes: 1 * 1024 * 1024, // 1 MB
	}
}

// RunCpp compiles and executes C++ code inside a sandboxed environment.
// The `input` string is fed to the program via stdin.
func RunCpp(code, input string, cfg *Config) *Result {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	tmpDir, err := os.MkdirTemp("", "sandbox-*")
	if err != nil {
		return &Result{Status: "error", Stderr: "Failed to create sandbox: " + err.Error()}
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chmod(tmpDir, 0777); err != nil {
		return &Result{Status: "error", Stderr: "Failed to set sandbox permissions"}
	}

	srcPath := filepath.Join(tmpDir, "main.cpp")
	if err := os.WriteFile(srcPath, []byte(code), 0644); err != nil {
		return &Result{Status: "error", Stderr: "Failed to write source: " + err.Error()}
	}

	inputPath := filepath.Join(tmpDir, "input.txt")
	if err := os.WriteFile(inputPath, []byte(input), 0644); err != nil {
		return &Result{Status: "error", Stderr: "Failed to write input: " + err.Error()}
	}

	binPath := filepath.Join(tmpDir, "main")

	compileResult, compileMs := compile(tmpDir, srcPath, binPath)
	if compileResult != nil {
		return compileResult
	}
	os.Chmod(binPath, 0755)

	result := execute(tmpDir, binPath, inputPath, cfg)
	result.CompileTimeMs = compileMs
	return result
}

// RunCppWithArgs compiles C++ code, then runs the binary with `argv` passed as
// command-line arguments and an empty stdin. This is the correct interface for
// testlib.h-style generators which receive the seed and parameters via argc/argv.
//
// `argv` should be a space-separated string of arguments, e.g. "100 200 42".
// Each call re-uses `binPath` when it is already compiled (non-empty string);
// pass an empty string to compile fresh.
func RunCppWithArgs(code, argv string, cfg *Config) *Result {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	tmpDir, err := os.MkdirTemp("", "sandbox-gen-*")
	if err != nil {
		return &Result{Status: "error", Stderr: "Failed to create sandbox: " + err.Error()}
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chmod(tmpDir, 0777); err != nil {
		return &Result{Status: "error", Stderr: "Failed to set sandbox permissions"}
	}

	srcPath := filepath.Join(tmpDir, "main.cpp")
	if err := os.WriteFile(srcPath, []byte(code), 0644); err != nil {
		return &Result{Status: "error", Stderr: "Failed to write source: " + err.Error()}
	}

	binPath := filepath.Join(tmpDir, "main")
	compileResult, compileMs := compile(tmpDir, srcPath, binPath)
	if compileResult != nil {
		return compileResult
	}
	os.Chmod(binPath, 0755)

	result := executeWithArgs(tmpDir, binPath, argv, cfg)
	result.CompileTimeMs = compileMs
	return result
}

// CompileOnly compiles the given C++ source and returns (binPath, errorResult).
// The caller is responsible for cleaning up tmpDir.
func CompileOnly(code string) (tmpDir, binPath string, compileMs int64, errResult *Result) {
	var err error
	tmpDir, err = os.MkdirTemp("", "sandbox-cmp-*")
	if err != nil {
		return "", "", 0, &Result{Status: "error", Stderr: "Failed to create sandbox: " + err.Error()}
	}

	if err := os.Chmod(tmpDir, 0777); err != nil {
		os.RemoveAll(tmpDir)
		return "", "", 0, &Result{Status: "error", Stderr: "Failed to set sandbox permissions"}
	}

	srcPath := filepath.Join(tmpDir, "main.cpp")
	if err := os.WriteFile(srcPath, []byte(code), 0644); err != nil {
		os.RemoveAll(tmpDir)
		return "", "", 0, &Result{Status: "error", Stderr: "Failed to write source: " + err.Error()}
	}

	binPath = filepath.Join(tmpDir, "main")
	er, ms := compile(tmpDir, srcPath, binPath)
	if er != nil {
		os.RemoveAll(tmpDir)
		return "", "", ms, er
	}
	os.Chmod(binPath, 0755)
	return tmpDir, binPath, ms, nil
}

// RunCompiledWithArgs runs an already-compiled binary with argv as CLI args.
func RunCompiledWithArgs(tmpDir, binPath, argv string, cfg *Config) *Result {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return executeWithArgs(tmpDir, binPath, argv, cfg)
}

// RunCompiledWithStdin runs an already-compiled binary feeding input via stdin.
func RunCompiledWithStdin(tmpDir, binPath, input string, cfg *Config) *Result {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	inputPath := filepath.Join(tmpDir, "input_run.txt")
	if err := os.WriteFile(inputPath, []byte(input), 0644); err != nil {
		return &Result{Status: "error", Stderr: "Failed to write input: " + err.Error()}
	}
	return execute(tmpDir, binPath, inputPath, cfg)
}

// compile runs g++ and returns (errorResult, compileTimeMs).
// A nil errorResult means compilation succeeded.
//
// Compilation is sandboxed with its own (looser) rlimits so that malicious
// template-metaprograms / #include bombs cannot OOM the host during a
// submission. The compiler runs as the sandbox user when available.
func compile(tmpDir, srcPath, binPath string) (*Result, int64) {
	return compileWithFlags(tmpDir, srcPath, binPath, nil)
}

// executeWithArgs runs the compiled binary with CLI arguments (no stdin).
// argv is a space-separated string; each token becomes a separate argument.
func executeWithArgs(tmpDir, binPath, argv string, cfg *Config) *Result {
	// Split argv on whitespace. This matches the existing behaviour of
	// passing it through a shell which would word-split on spaces. It is
	// NOT shell-safe (no quoting), but every caller today passes simple
	// numeric/alphanumeric tokens.
	extraArgs := strings.Fields(argv)
	return runSandboxed(tmpDir, binPath, extraArgs, strings.NewReader(""), cfg)
}

// execute runs the compiled binary fed from an input file.
func execute(tmpDir, binPath, inputPath string, cfg *Config) *Result {
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return &Result{Status: "error", Stderr: "Failed to open input"}
	}
	defer inputFile.Close()
	return runSandboxed(tmpDir, binPath, nil, inputFile, cfg)
}

// runSandboxed is the single sandboxed-exec primitive used by every caller.
// It applies kernel rlimits (via prlimit with ulimit fallback), a process
// group for group-kill, an optional uid drop, a wall-clock context deadline,
// and classifies the outcome into the status codes the rest of the backend
// expects ("success", "time_limit_exceeded", "memory_limit_exceeded",
// "runtime_error", "error").
//
//   - `args`  : extra argv to the child (may be nil/empty).
//   - `stdin` : reader attached to the child's stdin (may be nil).
//   - `cfg`   : per-submission limits (time, memory, output).
func runSandboxed(tmpDir, binPath string, args []string, stdin io.Reader, cfg *Config) *Result {
	// Wall-clock deadline slightly longer than the CPU budget so rlimits
	// fire first when the program is CPU-bound; the deadline still catches
	// purely blocked / sleeping children.
	ctx, cancel := context.WithTimeout(
		context.Background(),
		time.Duration(cfg.MaxTimeSec)*time.Second+500*time.Millisecond,
	)
	defer cancel()

	// Bound global concurrency — see SANDBOX_SLOTS docs.
	release := acquireSlot()
	defer release()

	spec := execLimits(cfg.MaxMemoryMB, cfg.MaxTimeSec+2) // +2s CPU, <0.5s wall

	var cmd *exec.Cmd
	if wrap := prlimitWrap(spec); wrap != nil {
		// argv = [prlimit …, binPath, args…]
		argv := append(append([]string{}, wrap...), binPath)
		argv = append(argv, args...)
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
	} else {
		// Fallback: /bin/sh with ulimit. Less reliable; warned at startup.
		shellCmd := ulimitFallback(spec) +
			"exec " + shellQuoteAll(append([]string{binPath}, args...))
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", shellCmd)
	}
	cmd.Dir = tmpDir
	cmd.Stdin = stdin

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, limit: cfg.MaxOutputBytes}
	cmd.Stderr = &limitedWriter{w: &stderr, limit: cfg.MaxOutputBytes}
	applySandboxAttrs(cmd)

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	result := &Result{
		Stdout:      stdout.String(),
		Stderr:      stderr.String(),
		TimeTakenMs: elapsed.Milliseconds(),
	}
	if cmd.ProcessState != nil {
		if rusage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			// rusage.Maxrss is in KB on Linux but in BYTES on macOS/BSD;
			// normalise to KB.
			if runtime.GOOS == "darwin" || runtime.GOOS == "freebsd" {
				result.MemoryUsedKB = rusage.Maxrss / 1024
			} else {
				result.MemoryUsedKB = rusage.Maxrss
			}
		}
	}

	// ── Classify the outcome ───────────────────────────────────────────

	// 1. Context deadline → TLE. Kill the whole process group so any
	//    children of the child also die.
	if ctx.Err() == context.DeadlineExceeded {
		result.Status = "time_limit_exceeded"
		result.Stderr = fmt.Sprintf("Time limit exceeded (%ds)", cfg.MaxTimeSec)
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return result
	}

	// 2. Memory exceeded (rusage is the authoritative signal when available).
	if result.MemoryUsedKB > int64(cfg.MaxMemoryMB)*1024 {
		result.Status = "memory_limit_exceeded"
		result.Stderr = fmt.Sprintf("Memory limit exceeded (%dMB)", cfg.MaxMemoryMB)
		return result
	}

	// 3. Non-zero exit / signal.
	if runErr != nil {
		if exitError, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exitError.ExitCode()
			if ws, ok := exitError.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				switch ws.Signal() {
				case syscall.SIGSEGV:
					result.Status = "runtime_error"
					result.Stderr = "Segmentation fault (SIGSEGV)"
				case syscall.SIGFPE:
					result.Status = "runtime_error"
					result.Stderr = "Floating point exception (SIGFPE)"
				case syscall.SIGABRT:
					result.Status = "runtime_error"
					result.Stderr = "Aborted (SIGABRT)"
				case syscall.SIGKILL:
					// SIGKILL usually means the OOM killer (rlimit AS) or our
					// context deadline. If rusage is over the limit it's
					// memory; otherwise treat as OOM too since the most
					// common cause is AS being exceeded.
					result.Status = "memory_limit_exceeded"
					result.Stderr = fmt.Sprintf("Memory limit exceeded (%dMB) — killed by OS", cfg.MaxMemoryMB)
				case syscall.SIGXCPU:
					result.Status = "time_limit_exceeded"
					result.Stderr = fmt.Sprintf("CPU time limit exceeded (%ds)", cfg.MaxTimeSec)
				case syscall.SIGXFSZ:
					result.Status = "runtime_error"
					result.Stderr = "File size limit exceeded"
				default:
					result.Status = "runtime_error"
					result.Stderr = fmt.Sprintf("Killed by signal: %s", ws.Signal())
				}
			} else {
				result.Status = "runtime_error"
				errMsg := result.Stderr
				if errMsg == "" {
					errMsg = "Runtime error (exit code: " + strconv.Itoa(result.ExitCode) + ")"
				}
				result.Stderr = strings.ReplaceAll(errMsg, tmpDir+"/", "")
			}
		} else {
			result.Status = "runtime_error"
			result.Stderr = runErr.Error()
		}
		return result
	}

	result.Status = "success"
	result.ExitCode = 0
	return result
}

// CleanupDir removes a temp directory created by CompileOnly.
func CleanupDir(dir string) {
	if dir != "" {
		os.RemoveAll(dir)
	}
}

// RunGeneratorWithSeed compiles the generator with -DSEED=<seed> injected at
// compile time, then runs it with:
//   - seed as argv[1]   (testlib.h / srand(atoi(argv[1])) style)
//   - seed written to stdin  (cin >> seed style)
//
// This covers every common generator seeding convention including srand(SEED)
// (where SEED is the compile-time macro), srand(atoi(argv[1])), and reading
// the seed from stdin.  Extra user args are prepended before the seed in argv.
//
// The binary is compiled fresh for every call so the macro value is unique.
func RunGeneratorWithSeed(code, extraArgs string, seed int, cfg *Config) *Result {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	tmpDir, err := os.MkdirTemp("", "sandbox-genseed-*")
	if err != nil {
		return &Result{Status: "error", Stderr: "Failed to create sandbox: " + err.Error()}
	}
	defer os.RemoveAll(tmpDir)

	if err := os.Chmod(tmpDir, 0777); err != nil {
		return &Result{Status: "error", Stderr: "Failed to set sandbox permissions"}
	}

	srcPath := filepath.Join(tmpDir, "main.cpp")
	if err := os.WriteFile(srcPath, []byte(code), 0644); err != nil {
		return &Result{Status: "error", Stderr: "Failed to write source: " + err.Error()}
	}

	binPath := filepath.Join(tmpDir, "main")

	// Compile with the seed injected as a preprocessor macro so that
	// generators using `srand(SEED)` get a unique value per invocation.
	seedDefine := fmt.Sprintf("-DSEED=%d", seed)
	compResult, compMs := compileWithFlags(tmpDir, srcPath, binPath, []string{seedDefine})
	if compResult != nil {
		return compResult
	}
	compResult = &Result{CompileTimeMs: compMs} // reuse var for timing only
	_ = compResult
	os.Chmod(binPath, 0755)

	// Build argv: [extraArgs] [seed]
	argv := strconv.Itoa(seed)
	if extraArgs != "" {
		argv = extraArgs + " " + argv
	}

	// Write seed to stdin so `cin >> seed` style generators also work.
	inputPath := filepath.Join(tmpDir, "seed.txt")
	if err := os.WriteFile(inputPath, []byte(strconv.Itoa(seed)+"\n"), 0644); err != nil {
		return &Result{Status: "error", Stderr: "Failed to write seed input: " + err.Error()}
	}

	// Run with both argv and stdin containing the seed.
	return executeGeneratorWithArgvAndStdin(tmpDir, binPath, argv, inputPath, cfg)
}

// compileWithFlags is like compile but accepts extra compiler flags.
//
// Compilation is sandboxed: bounded memory/CPU via prlimit (with ulimit
// fallback), 30s wall-clock deadline, 1 MB output cap per stream, and runs
// as the sandbox user when the backend has privileges to drop uid.
func compileWithFlags(tmpDir, srcPath, binPath string, extraFlags []string) (*Result, int64) {
	// Serialize against other heavy sandbox operations so a flood of
	// submissions can't OOM the host via parallel g++ invocations.
	release := acquireSlot()
	defer release()

	const compileWallSec = 30
	ctx, cancel := context.WithTimeout(context.Background(), compileWallSec*time.Second)
	defer cancel()

	gxxArgs := []string{"-o", binPath, srcPath, "-std=c++17", "-O2", "-Wall", "-Wextra", "-DONLINE_JUDGE", "-lm"}
	gxxArgs = append(gxxArgs, extraFlags...)

	// Build the argv: prlimit wrapper (if available) -> g++ -> flags.
	// CPU budget for compile is a few seconds under wall so ulimit -t fires
	// slightly before the context deadline.
	spec := compileLimits(compileWallSec - 2)
	wrap := prlimitWrap(spec)
	var cmd *exec.Cmd
	if wrap != nil {
		full := append(append([]string{}, wrap...), "g++")
		full = append(full, gxxArgs...)
		cmd = exec.CommandContext(ctx, full[0], full[1:]...)
	} else {
		// Fall back to a shell-based wrapper. ulimit isn't as reliable but
		// it's better than nothing; we log at startup so ops knows.
		shellCmd := ulimitFallback(spec) + "exec g++ " + shellQuoteAll(gxxArgs)
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", shellCmd)
	}
	cmd.Dir = tmpDir
	applySandboxAttrs(cmd)

	var stdout, stderr bytes.Buffer
	// Cap compile output too — a malicious program could emit millions of
	// warnings and OOM the server collecting them.
	cmd.Stdout = &limitedWriter{w: &stdout, limit: 1 * 1024 * 1024}
	cmd.Stderr = &limitedWriter{w: &stderr, limit: 1 * 1024 * 1024}

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	if ctx.Err() == context.DeadlineExceeded {
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return &Result{
			Status:        "compilation_error",
			Stderr:        fmt.Sprintf("Compilation timed out (%ds limit)", compileWallSec),
			CompileTimeMs: elapsed,
		}, elapsed
	}
	if err != nil {
		errMsg := stderr.String()
		errMsg = strings.ReplaceAll(errMsg, tmpDir+"/", "")
		errMsg = strings.ReplaceAll(errMsg, tmpDir, "")
		// If the rlimit killed g++ with SIGKILL/SIGXCPU the stderr may be
		// empty; surface a helpful message in that case.
		if errMsg == "" {
			if exitErr, ok := err.(*exec.ExitError); ok {
				if ws, ok2 := exitErr.Sys().(syscall.WaitStatus); ok2 && ws.Signaled() {
					switch ws.Signal() {
					case syscall.SIGKILL:
						errMsg = "Compilation exceeded memory limit (killed by OS)"
					case syscall.SIGXCPU:
						errMsg = "Compilation exceeded CPU time limit"
					default:
						errMsg = fmt.Sprintf("Compiler killed by signal: %s", ws.Signal())
					}
				}
			}
		}
		return &Result{Status: "compilation_error", Stderr: errMsg, CompileTimeMs: elapsed}, elapsed
	}
	return nil, elapsed
}

// applySandboxAttrs sets the common SysProcAttr used for every sandboxed
// child: new process group (so we can SIGKILL the whole tree on timeout)
// and uid drop when we have privilege to do so.
func applySandboxAttrs(cmd *exec.Cmd) {
	attrs := &syscall.SysProcAttr{Setpgid: true}
	if cred := getSandboxCredential(); cred != nil {
		attrs.Credential = cred
	}
	cmd.SysProcAttr = attrs
}

// shellQuoteAll joins args suitable for /bin/sh -c. Each arg is single-quoted
// with embedded single quotes escaped. Only used for the ulimit-fallback
// path when prlimit is unavailable; prlimit uses argv directly and doesn't
// need shell quoting.
func shellQuoteAll(args []string) string {
	var b bytes.Buffer
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('\'')
		b.WriteString(strings.ReplaceAll(a, "'", `'\''`))
		b.WriteByte('\'')
	}
	return b.String()
}

// executeGeneratorWithArgvAndStdin runs the generator with both CLI args and
// stdin pre-seeded, routing through the shared sandbox primitive.
func executeGeneratorWithArgvAndStdin(tmpDir, binPath, argv, inputPath string, cfg *Config) *Result {
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return &Result{Status: "error", Stderr: "Failed to open seed input"}
	}
	defer inputFile.Close()
	return runSandboxed(tmpDir, binPath, strings.Fields(argv), inputFile, cfg)
}

// limitedWriter caps how much data can be buffered (prevents output flooding).
type limitedWriter struct {
	w       *bytes.Buffer
	limit   int
	written int
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	remaining := lw.limit - lw.written
	if remaining <= 0 {
		return len(p), nil // silently discard excess
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	n, err := lw.w.Write(p)
	lw.written += n
	return n, err
}
