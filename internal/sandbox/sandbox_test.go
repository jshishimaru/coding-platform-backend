package sandbox

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// These tests exercise the sandbox end-to-end on whatever platform they run.
// They don't require prlimit (the fallback ulimit path is fine for asserting
// that *some* limit fires) but they do need g++ on PATH.

func requireGxx(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ not available; skipping sandbox integration test")
	}
}

// A well-behaved program should succeed cleanly and produce its stdout.
func TestSandbox_NormalProgramSucceeds(t *testing.T) {
	requireGxx(t)
	res := RunCpp(
		`#include <iostream>
		int main(){ std::cout << "hi\n"; return 0; }`,
		"", DefaultConfig(),
	)
	if res.Status != "success" {
		t.Fatalf("expected success, got %q stderr=%q", res.Status, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "hi" {
		t.Fatalf("unexpected stdout %q", res.Stdout)
	}
}

// A program that spins on the CPU must hit TLE within a bounded time —
// never hang the test suite.
func TestSandbox_TimeLimitExceeded(t *testing.T) {
	requireGxx(t)
	cfg := &Config{MaxTimeSec: 1, MaxMemoryMB: 128, MaxOutputBytes: 1 << 20}

	start := time.Now()
	res := RunCpp(
		`int main(){ while(1){} }`,
		"", cfg,
	)
	elapsed := time.Since(start)

	if res.Status != "time_limit_exceeded" {
		t.Fatalf("expected TLE, got %q (%q)", res.Status, res.Stderr)
	}
	// Hard ceiling: wall-clock deadline is cpu+0.5s. Add a generous slack
	// for CI scheduling jitter.
	if elapsed > 6*time.Second {
		t.Fatalf("TLE took %v — process not killed promptly", elapsed)
	}
}

// Allocating far more than the memory limit must NOT succeed. We accept any
// failing status ("memory_limit_exceeded" or "runtime_error") since Linux
// vs macOS allocator behaviour differs — the invariant is that it doesn't
// silently run through with status=success.
func TestSandbox_MemoryBombDoesNotSucceed(t *testing.T) {
	requireGxx(t)
	cfg := &Config{MaxTimeSec: 3, MaxMemoryMB: 64, MaxOutputBytes: 1 << 20}

	res := RunCpp(
		`#include <vector>
		int main(){
		    // Try to reserve ~512 MB; must be far above the 64 MB limit.
		    std::vector<char> v;
		    for (long long i = 0; i < 512; ++i) {
		        v.resize(v.size() + 1024*1024, 'x');
		    }
		    return 0;
		}`,
		"", cfg,
	)
	if res.Status == "success" {
		t.Fatalf("memory bomb ran to completion — limits NOT enforced (stdout=%q, stderr=%q, mem=%d KB)",
			res.Stdout, res.Stderr, res.MemoryUsedKB)
	}
}

// A program that prints megabytes of output must have its output truncated
// and must not hang collecting it.
func TestSandbox_OutputCapped(t *testing.T) {
	requireGxx(t)
	cfg := &Config{MaxTimeSec: 3, MaxMemoryMB: 128, MaxOutputBytes: 16 * 1024}

	res := RunCpp(
		`#include <cstdio>
		int main(){
		    for (long long i = 0; i < 10000000LL; ++i) putchar('a');
		    return 0;
		}`,
		"", cfg,
	)
	if len(res.Stdout) > cfg.MaxOutputBytes+1024 {
		t.Fatalf("stdout not capped: got %d bytes, limit %d",
			len(res.Stdout), cfg.MaxOutputBytes)
	}
}

// Prlimit detection: on a host where prlimit is absent we should fall back
// gracefully without panicking. This just asserts the helpers are callable
// and produce a non-empty fallback fragment.
func TestSandbox_UlimitFallback(t *testing.T) {
	spec := execLimits(64, 2)
	frag := ulimitFallback(spec)
	if frag == "" {
		t.Fatalf("expected non-empty ulimit fragment")
	}
	for _, want := range []string{"ulimit -v", "ulimit -t", "ulimit -u"} {
		if !strings.Contains(frag, want) {
			t.Errorf("ulimit fragment missing %q: %s", want, frag)
		}
	}
}
