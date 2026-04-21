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

// executeWithArgs runs the compiled binary with CLI arguments (no stdin).
// argv is a space-separated string; each token becomes a separate argument.
func executeWithArgs(tmpDir, binPath, argv string, cfg *Config) *Result {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		time.Duration(cfg.MaxTimeSec)*time.Second+500*time.Millisecond,
	)
	defer cancel()

	memLimitKB := cfg.MaxMemoryMB * 1024
	cpuTimeSec := cfg.MaxTimeSec + 2

	// Quote the binary path and append each argument properly.
	// We construct the shell command so ulimits fire before exec.
	shellCmd := fmt.Sprintf(
		"ulimit -v %d 2>/dev/null; ulimit -f 10240 2>/dev/null; ulimit -u 64 2>/dev/null; ulimit -t %d 2>/dev/null; exec %s %s",
		memLimitKB, cpuTimeSec, binPath, argv,
	)

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", shellCmd)
	cmd.Dir = tmpDir

	// Empty stdin
	cmd.Stdin = strings.NewReader("")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, limit: cfg.MaxOutputBytes}
	cmd.Stderr = &limitedWriter{w: &stderr, limit: cfg.MaxOutputBytes}

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

	if cmd.ProcessState != nil {
		if rusage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			result.MemoryUsedKB = rusage.Maxrss
		}
	}

	if ctx.Err() == context.DeadlineExceeded {
		result.Status = "time_limit_exceeded"
		result.Stderr = fmt.Sprintf("Time limit exceeded (%ds)", cfg.MaxTimeSec)
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return result
	}

	if runErr != nil {
		if exitError, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exitError.ExitCode()
			if ws, ok := exitError.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				result.Status = "runtime_error"
				result.Stderr = fmt.Sprintf("Killed by signal: %s", ws.Signal())
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
func compileWithFlags(tmpDir, srcPath, binPath string, extraFlags []string) (*Result, int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	args := []string{"-o", binPath, srcPath, "-std=c++17", "-O2", "-Wall", "-Wextra", "-DONLINE_JUDGE", "-lm"}
	args = append(args, extraFlags...)

	cmd := exec.CommandContext(ctx, "g++", args...)
	cmd.Dir = tmpDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	if ctx.Err() == context.DeadlineExceeded {
		return &Result{Status: "compilation_error", Stderr: "Compilation timed out (30s limit)", CompileTimeMs: elapsed}, elapsed
	}
	if err != nil {
		errMsg := stderr.String()
		errMsg = strings.ReplaceAll(errMsg, tmpDir+"/", "")
		errMsg = strings.ReplaceAll(errMsg, tmpDir, "")
		return &Result{Status: "compilation_error", Stderr: errMsg, CompileTimeMs: elapsed}, elapsed
	}
	return nil, elapsed
}

// executeGeneratorWithArgvAndStdin runs the generator with both CLI args and
// stdin pre-seeded.
func executeGeneratorWithArgvAndStdin(tmpDir, binPath, argv, inputPath string, cfg *Config) *Result {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		time.Duration(cfg.MaxTimeSec)*time.Second+500*time.Millisecond,
	)
	defer cancel()

	memLimitKB := cfg.MaxMemoryMB * 1024
	cpuTimeSec  := cfg.MaxTimeSec + 2

	shellCmd := fmt.Sprintf(
		"ulimit -v %d 2>/dev/null; ulimit -f 10240 2>/dev/null; ulimit -u 64 2>/dev/null; ulimit -t %d 2>/dev/null; exec %s %s",
		memLimitKB, cpuTimeSec, binPath, argv,
	)

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", shellCmd)
	cmd.Dir = tmpDir

	inputFile, err := os.Open(inputPath)
	if err != nil {
		return &Result{Status: "error", Stderr: "Failed to open seed input"}
	}
	defer inputFile.Close()
	cmd.Stdin = inputFile

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, limit: cfg.MaxOutputBytes}
	cmd.Stderr = &limitedWriter{w: &stderr, limit: cfg.MaxOutputBytes}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Credential: &syscall.Credential{Uid: 1001, Gid: 1001},
	}

	start   := time.Now()
	runErr  := cmd.Run()
	elapsed := time.Since(start)

	result := &Result{
		Stdout:      stdout.String(),
		Stderr:      stderr.String(),
		TimeTakenMs: elapsed.Milliseconds(),
	}
	if cmd.ProcessState != nil {
		if rusage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			result.MemoryUsedKB = rusage.Maxrss
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		result.Status = "time_limit_exceeded"
		result.Stderr = fmt.Sprintf("Time limit exceeded (%ds)", cfg.MaxTimeSec)
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return result
	}
	if runErr != nil {
		if exitError, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exitError.ExitCode()
			result.Status = "runtime_error"
			errMsg := result.Stderr
			if errMsg == "" {
				errMsg = "Runtime error (exit code: " + strconv.Itoa(result.ExitCode) + ")"
			}
			result.Stderr = strings.ReplaceAll(errMsg, tmpDir+"/", "")
		} else {
			result.Status = "runtime_error"
			result.Stderr = runErr.Error()
		}
		return result
	}
	result.Status = "success"
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
