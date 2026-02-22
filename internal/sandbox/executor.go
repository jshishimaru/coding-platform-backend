package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
func RunCpp(code, input string, cfg *Config) *Result {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Create an isolated temp directory
	tmpDir, err := os.MkdirTemp("", "sandbox-*")
	if err != nil {
		return &Result{Status: "error", Stderr: "Failed to create sandbox: " + err.Error()}
	}
	defer os.RemoveAll(tmpDir)

	// Make temp dir accessible by sandbox user (uid 1001)
	if err := os.Chmod(tmpDir, 0777); err != nil {
		return &Result{Status: "error", Stderr: "Failed to set sandbox permissions"}
	}

	// Write source code
	srcPath := filepath.Join(tmpDir, "main.cpp")
	if err := os.WriteFile(srcPath, []byte(code), 0644); err != nil {
		return &Result{Status: "error", Stderr: "Failed to write source: " + err.Error()}
	}

	// Write stdin input
	inputPath := filepath.Join(tmpDir, "input.txt")
	if err := os.WriteFile(inputPath, []byte(input), 0644); err != nil {
		return &Result{Status: "error", Stderr: "Failed to write input: " + err.Error()}
	}

	binPath := filepath.Join(tmpDir, "main")

	// ── Phase 1: Compile ───────────────────────────────────────────────
	compileResult, compileMs := compile(tmpDir, srcPath, binPath)
	if compileResult != nil {
		return compileResult
	}

	// Ensure the binary is executable by the sandbox user
	os.Chmod(binPath, 0755)

	// ── Phase 2: Execute ───────────────────────────────────────────────
	result := execute(tmpDir, binPath, inputPath, cfg)
	result.CompileTimeMs = compileMs
	return result
}

// compile runs g++ and returns (errorResult, compileTimeMs).
// A nil errorResult means compilation succeeded.
func compile(tmpDir, srcPath, binPath string) (*Result, int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "g++",
		"-o", binPath, srcPath,
		"-std=c++17", "-O2",
		"-Wall", "-Wextra",
		"-DONLINE_JUDGE",
		"-lm",
	)
	cmd.Dir = tmpDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	if ctx.Err() == context.DeadlineExceeded {
		return &Result{
			Status:        "compilation_error",
			Stderr:        "Compilation timed out (30s limit)",
			CompileTimeMs: elapsed,
		}, elapsed
	}

	if err != nil {
		// Sanitize paths so users don't see temp directory info
		errMsg := stderr.String()
		errMsg = strings.ReplaceAll(errMsg, tmpDir+"/", "")
		errMsg = strings.ReplaceAll(errMsg, tmpDir, "")
		return &Result{
			Status:        "compilation_error",
			Stderr:        errMsg,
			CompileTimeMs: elapsed,
		}, elapsed
	}

	return nil, elapsed
}

// execute runs the compiled binary with resource limits and isolation.
func execute(tmpDir, binPath, inputPath string, cfg *Config) *Result {
	// Wall-clock timeout with a small buffer so ulimit fires first on CPU
	ctx, cancel := context.WithTimeout(
		context.Background(),
		time.Duration(cfg.MaxTimeSec)*time.Second+500*time.Millisecond,
	)
	defer cancel()

	memLimitKB := cfg.MaxMemoryMB * 1024
	cpuTimeSec := cfg.MaxTimeSec + 2 // CPU-time limit slightly above wall-clock

	// Shell wrapper sets resource limits before exec-ing the binary.
	// ulimit -v : virtual memory (KB)
	// ulimit -f : max file size (KB) – 10 MB
	// ulimit -u : max user processes – fork-bomb protection
	// ulimit -t : CPU time (seconds)
	shellCmd := fmt.Sprintf(
		"ulimit -v %d 2>/dev/null; ulimit -f 10240 2>/dev/null; ulimit -u 64 2>/dev/null; ulimit -t %d 2>/dev/null; exec %s",
		memLimitKB, cpuTimeSec, binPath,
	)

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", shellCmd)
	cmd.Dir = tmpDir

	// Feed stdin from the input file
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return &Result{Status: "error", Stderr: "Failed to open input"}
	}
	defer inputFile.Close()
	cmd.Stdin = inputFile

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, limit: cfg.MaxOutputBytes}
	cmd.Stderr = &limitedWriter{w: &stderr, limit: cfg.MaxOutputBytes}

	// Process-group isolation + run as sandbox user (uid/gid 1001)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Credential: &syscall.Credential{
			Uid: 1001,
			Gid: 1001,
		},
	}

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	result := &Result{
		Stdout:      stdout.String(),
		Stderr:      stderr.String(),
		TimeTakenMs: elapsed.Milliseconds(),
	}

	// Collect peak memory from kernel rusage
	if cmd.ProcessState != nil {
		if rusage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			result.MemoryUsedKB = rusage.Maxrss
		}
	}

	// ── Classify the outcome ─────────────────────────────────────────

	// 1. Context deadline  →  TLE
	if ctx.Err() == context.DeadlineExceeded {
		result.Status = "time_limit_exceeded"
		result.Stderr = fmt.Sprintf("Time limit exceeded (%ds)", cfg.MaxTimeSec)
		// Kill entire process group
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return result
	}

	// 2. Memory exceeded
	if result.MemoryUsedKB > int64(cfg.MaxMemoryMB)*1024 {
		result.Status = "memory_limit_exceeded"
		result.Stderr = fmt.Sprintf("Memory limit exceeded (%dMB)", cfg.MaxMemoryMB)
		return result
	}

	// 3. Non-zero exit / signal
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
					result.Status = "memory_limit_exceeded"
					result.Stderr = fmt.Sprintf("Memory limit exceeded (%dMB) — killed by OS", cfg.MaxMemoryMB)
				case syscall.SIGXCPU:
					result.Status = "time_limit_exceeded"
					result.Stderr = fmt.Sprintf("CPU time limit exceeded (%ds)", cfg.MaxTimeSec)
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

	// 4. Clean exit
	result.Status = "success"
	result.ExitCode = 0
	return result
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
