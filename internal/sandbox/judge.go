package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultCheckerCode is a token-by-token comparator that handles whitespace differences.
const DefaultCheckerCode = `#include <fstream>
#include <string>
using namespace std;
int main(int argc, char* argv[]) {
    if (argc != 4) return 2;
    ifstream fexp(argv[2]), fact(argv[3]);
    if (!fexp.is_open() || !fact.is_open()) return 2;
    string te, ta;
    while (fexp >> te) {
        if (!(fact >> ta)) return 1;
        if (te != ta) return 1;
    }
    if (fact >> ta) return 1;
    return 0;
}
`

// JudgeRequest describes a code submission to be judged.
type JudgeRequest struct {
	Code          string
	Language      string
	TestCases     []TestCaseInput
	CheckerCode   string
	TimeLimitMs   int
	MemoryLimitMB int
}

// TestCaseInput is a single test case to run against.
type TestCaseInput struct {
	ID             int
	Input          string
	ExpectedOutput string
	IsSample       bool
}

// TestCaseResult is the outcome of a single test case.
type TestCaseResult struct {
	TestCaseID    int    `json:"test_case_id"`
	Status        string `json:"status"`
	Stdout        string `json:"stdout,omitempty"`
	Stderr        string `json:"stderr,omitempty"`
	TimeTakenMs   int64  `json:"time_taken_ms"`
	MemoryUsedKB  int64  `json:"memory_used_kb"`
	CheckerOutput string `json:"checker_output,omitempty"`
	IsSample      bool   `json:"is_sample"`
}

// JudgeResult is the aggregated outcome of running all test cases.
type JudgeResult struct {
	Status          string           `json:"status"`
	CompileTimeMs   int64            `json:"compile_time_ms"`
	CompileError    string           `json:"compile_error,omitempty"`
	TestCaseResults []TestCaseResult `json:"test_case_results"`
	PassedCount     int              `json:"passed_count"`
	TotalCount      int              `json:"total_count"`
	MaxTimeMs       int64            `json:"max_time_ms"`
	MaxMemoryKB     int64            `json:"max_memory_kb"`
}

// Judge compiles user code once, compiles checker once, then evaluates each test case.
func Judge(req *JudgeRequest) *JudgeResult {
	tmpDir, err := os.MkdirTemp("", "judge-*")
	if err != nil {
		return &JudgeResult{Status: "error", CompileError: "Failed to create judge directory"}
	}
	defer os.RemoveAll(tmpDir)
	os.Chmod(tmpDir, 0777)

	// ── Compile user code ──────────────────────────────────────────
	userSrc := filepath.Join(tmpDir, "solution.cpp")
	userBin := filepath.Join(tmpDir, "solution")
	if err := os.WriteFile(userSrc, []byte(req.Code), 0644); err != nil {
		return &JudgeResult{Status: "error", CompileError: "Failed to write source"}
	}

	compileResult, compileMs := compile(tmpDir, userSrc, userBin)
	if compileResult != nil {
		return &JudgeResult{
			Status:        "compilation_error",
			CompileTimeMs: compileMs,
			CompileError:  compileResult.Stderr,
			TotalCount:    len(req.TestCases),
		}
	}
	os.Chmod(userBin, 0755)

	// ── Compile checker ────────────────────────────────────────────
	checkerCode := req.CheckerCode
	if strings.TrimSpace(checkerCode) == "" {
		checkerCode = DefaultCheckerCode
	}
	checkerSrc := filepath.Join(tmpDir, "checker.cpp")
	checkerBin := filepath.Join(tmpDir, "checker")
	if err := os.WriteFile(checkerSrc, []byte(checkerCode), 0644); err != nil {
		return &JudgeResult{Status: "error", CompileError: "Failed to write checker"}
	}

	checkerCompile, _ := compile(tmpDir, checkerSrc, checkerBin)
	if checkerCompile != nil {
		return &JudgeResult{
			Status:       "error",
			CompileError: "Checker compilation failed: " + checkerCompile.Stderr,
		}
	}
	os.Chmod(checkerBin, 0755)

	// ── Execute each test case ─────────────────────────────────────
	timeLimitSec := (req.TimeLimitMs + 999) / 1000
	if timeLimitSec <= 0 {
		timeLimitSec = 2
	}
	cfg := &Config{
		MaxTimeSec:     timeLimitSec,
		MaxMemoryMB:    req.MemoryLimitMB,
		MaxOutputBytes: 1024 * 1024,
	}
	if cfg.MaxMemoryMB <= 0 {
		cfg.MaxMemoryMB = 256
	}

	results := make([]TestCaseResult, 0, len(req.TestCases))
	passedCount := 0
	var maxTime int64
	var maxMem int64
	firstFailStatus := ""

	for i, tc := range req.TestCases {
		tcResult := runTestCase(tmpDir, userBin, checkerBin, tc, cfg, i)
		results = append(results, tcResult)

		if tcResult.Status == "accepted" {
			passedCount++
		} else if firstFailStatus == "" {
			firstFailStatus = tcResult.Status
		}
		if tcResult.TimeTakenMs > maxTime {
			maxTime = tcResult.TimeTakenMs
		}
		if tcResult.MemoryUsedKB > maxMem {
			maxMem = tcResult.MemoryUsedKB
		}
	}

	overallStatus := "accepted"
	if firstFailStatus != "" {
		overallStatus = firstFailStatus
	}

	return &JudgeResult{
		Status:          overallStatus,
		CompileTimeMs:   compileMs,
		TestCaseResults: results,
		PassedCount:     passedCount,
		TotalCount:      len(req.TestCases),
		MaxTimeMs:       maxTime,
		MaxMemoryKB:     maxMem,
	}
}

// runTestCase executes user code with one test case, then runs the checker.
func runTestCase(tmpDir, userBin, checkerBin string, tc TestCaseInput, cfg *Config, idx int) TestCaseResult {
	// Write input
	inputFile := filepath.Join(tmpDir, fmt.Sprintf("input_%d.txt", idx))
	os.WriteFile(inputFile, []byte(tc.Input), 0644)

	// Run user code
	execResult := execute(tmpDir, userBin, inputFile, cfg)

	if execResult.Status != "success" {
		return TestCaseResult{
			TestCaseID:   tc.ID,
			Status:       execResult.Status,
			Stderr:       execResult.Stderr,
			TimeTakenMs:  execResult.TimeTakenMs,
			MemoryUsedKB: execResult.MemoryUsedKB,
			IsSample:     tc.IsSample,
		}
	}

	// Write expected and actual output for checker
	expectedFile := filepath.Join(tmpDir, fmt.Sprintf("expected_%d.txt", idx))
	actualFile := filepath.Join(tmpDir, fmt.Sprintf("actual_%d.txt", idx))
	os.WriteFile(expectedFile, []byte(tc.ExpectedOutput), 0644)
	os.WriteFile(actualFile, []byte(execResult.Stdout), 0644)

	// Run checker
	exitCode, checkerOut := runChecker(tmpDir, checkerBin, inputFile, expectedFile, actualFile)

	status := "accepted"
	if exitCode != 0 {
		status = "wrong_answer"
	}

	return TestCaseResult{
		TestCaseID:    tc.ID,
		Status:        status,
		Stdout:        execResult.Stdout,
		TimeTakenMs:   execResult.TimeTakenMs,
		MemoryUsedKB:  execResult.MemoryUsedKB,
		CheckerOutput: checkerOut,
		IsSample:      tc.IsSample,
	}
}

// runChecker executes the checker binary with 3 file arguments.
// Returns (exitCode, stdout/stderr output).
func runChecker(tmpDir, checkerBin, inputFile, expectedFile, actualFile string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, checkerBin, inputFile, expectedFile, actualFile)
	cmd.Dir = tmpDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := strings.TrimSpace(stdout.String())
	if output == "" {
		output = strings.TrimSpace(stderr.String())
	}

	if ctx.Err() == context.DeadlineExceeded {
		return 2, "Checker timed out"
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), output
		}
		return 2, "Checker error: " + err.Error()
	}

	return 0, output
}
