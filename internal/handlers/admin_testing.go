package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/coding-platform/backend/internal/sandbox"
)

// ──────────────────────────────────────────────────────────
// Request / Response Types
// ──────────────────────────────────────────────────────────

type TestSolutionRequest struct {
	SolutionID int `json:"solution_id" binding:"required"`
}

type TestSolutionResponse struct {
	SolutionID  int                `json:"solution_id"`
	Verdict     string             `json:"verdict"`
	PassedCount int                `json:"passed_count"`
	TotalCount  int                `json:"total_count"`
	MaxTimeMs   int64              `json:"max_time_ms"`
	MaxMemoryKB int64              `json:"max_memory_kb"`
	Results     []TestResultDetail `json:"results"`
}

type TestResultDetail struct {
	TestCaseID     int    `json:"test_case_id"`
	OrderIndex     int    `json:"order_index"`
	Verdict        string `json:"verdict"`
	TimeTakenMs    int64  `json:"time_taken_ms"`
	MemoryUsedKB   int64  `json:"memory_used_kb"`
	Stdout         string `json:"stdout,omitempty"`
	Stderr         string `json:"stderr,omitempty"`
	ExpectedOutput string `json:"expected_output,omitempty"`
	IsSample       bool   `json:"is_sample"`
}

type StressTestRequest struct {
	MainSolutionID  int    `json:"main_solution_id" binding:"required"`
	BruteSolutionID int    `json:"brute_solution_id" binding:"required"`
	GeneratorID     int    `json:"generator_id" binding:"required"`
	ArgsTemplate    string `json:"args_template"`
	Iterations      int    `json:"iterations" binding:"required"`
}

type StressTestResponse struct {
	Success           bool   `json:"success"`
	IterationsRun     int    `json:"iterations_run"`
	FailedAtIteration *int   `json:"failed_at_iteration,omitempty"`
	FailedInput       string `json:"failed_input,omitempty"`
	MainOutput        string `json:"main_output,omitempty"`
	BruteOutput       string `json:"brute_output,omitempty"`
	Error             string `json:"error,omitempty"`
}

// ──────────────────────────────────────────────────────────
// Handlers
// ──────────────────────────────────────────────────────────

// AdminTestSolution runs a model solution against all test cases using the active checker.
func (h *Handler) AdminTestSolution(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	var req TestSolutionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()

	// Fetch solution code
	var solutionCode string
	err = h.DB.QueryRow(ctx,
		`SELECT source_code FROM app.problem_solutions WHERE id = $1 AND problem_id = $2`,
		req.SolutionID, problemID,
	).Scan(&solutionCode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Solution not found"})
		return
	}

	// Fetch problem limits and checker
	var timeLimitMs, memoryLimitMb int
	var checkerCode string
	err = h.DB.QueryRow(ctx,
		`SELECT time_limit_ms, memory_limit_mb, checker_code FROM app.problems WHERE id = $1`, problemID,
	).Scan(&timeLimitMs, &memoryLimitMb, &checkerCode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Problem not found"})
		return
	}

	// Check for an active checker override
	var activeCheckerCode string
	err = h.DB.QueryRow(ctx,
		`SELECT source_code FROM app.problem_checkers WHERE problem_id = $1 AND is_active = TRUE LIMIT 1`,
		problemID,
	).Scan(&activeCheckerCode)
	if err == nil && activeCheckerCode != "" {
		checkerCode = activeCheckerCode
	}

	// Fetch all test cases
	rows, err := h.DB.Query(ctx,
		`SELECT id, input, expected_output, is_sample, order_index
		 FROM app.test_cases WHERE problem_id = $1
		 ORDER BY order_index, id`, problemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	testCases := make([]sandbox.TestCaseInput, 0)
	orderMap := make(map[int]int)   // test case ID -> order_index
	sampleMap := make(map[int]bool) // test case ID -> is_sample
	expectedMap := make(map[int]string)
	for rows.Next() {
		var tc sandbox.TestCaseInput
		var orderIdx int
		var isSample bool
		if err := rows.Scan(&tc.ID, &tc.Input, &tc.ExpectedOutput, &isSample, &orderIdx); err != nil {
			continue
		}
		tc.IsSample = isSample
		testCases = append(testCases, tc)
		orderMap[tc.ID] = orderIdx
		sampleMap[tc.ID] = isSample
		expectedMap[tc.ID] = tc.ExpectedOutput
	}

	if len(testCases) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No test cases found"})
		return
	}

	// Use the sandbox judge
	judgeReq := &sandbox.JudgeRequest{
		Code:          solutionCode,
		Language:      "cpp",
		TestCases:     testCases,
		CheckerCode:   checkerCode,
		TimeLimitMs:   timeLimitMs,
		MemoryLimitMB: memoryLimitMb,
	}

	judgeResult := sandbox.Judge(judgeReq)

	// Build response
	results := make([]TestResultDetail, 0, len(judgeResult.TestCaseResults))
	for _, tcr := range judgeResult.TestCaseResults {
		detail := TestResultDetail{
			TestCaseID:     tcr.TestCaseID,
			OrderIndex:     orderMap[tcr.TestCaseID],
			Verdict:        tcr.Status,
			TimeTakenMs:    tcr.TimeTakenMs,
			MemoryUsedKB:   tcr.MemoryUsedKB,
			Stdout:         tcr.Stdout,
			Stderr:         tcr.Stderr,
			ExpectedOutput: expectedMap[tcr.TestCaseID],
			IsSample:       sampleMap[tcr.TestCaseID],
		}
		results = append(results, detail)
	}

	overallVerdict := judgeResult.Status
	if judgeResult.CompileError != "" {
		overallVerdict = "compilation_error"
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	h.logAudit(uid, "solution.test", "problem", problemID, map[string]interface{}{
		"solution_id": req.SolutionID,
		"verdict":     overallVerdict,
		"passed":      judgeResult.PassedCount,
		"total":       judgeResult.TotalCount,
	}, c.ClientIP())

	c.JSON(http.StatusOK, TestSolutionResponse{
		SolutionID:  req.SolutionID,
		Verdict:     overallVerdict,
		PassedCount: judgeResult.PassedCount,
		TotalCount:  judgeResult.TotalCount,
		MaxTimeMs:   judgeResult.MaxTimeMs,
		MaxMemoryKB: judgeResult.MaxMemoryKB,
		Results:     results,
	})
}

// AdminStressTest runs a stress test comparing two solutions.
func (h *Handler) AdminStressTest(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	var req StressTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Iterations < 1 || req.Iterations > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Iterations must be between 1 and 1000"})
		return
	}

	ctx := context.Background()

	// Fetch main solution code
	var mainCode string
	err = h.DB.QueryRow(ctx,
		`SELECT source_code FROM app.problem_solutions WHERE id = $1 AND problem_id = $2`,
		req.MainSolutionID, problemID,
	).Scan(&mainCode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Main solution not found"})
		return
	}

	// Fetch brute solution code
	var bruteCode string
	err = h.DB.QueryRow(ctx,
		`SELECT source_code FROM app.problem_solutions WHERE id = $1 AND problem_id = $2`,
		req.BruteSolutionID, problemID,
	).Scan(&bruteCode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Brute solution not found"})
		return
	}

	// Fetch generator code
	var genCode string
	err = h.DB.QueryRow(ctx,
		`SELECT source_code FROM app.problem_generators WHERE id = $1 AND problem_id = $2`,
		req.GeneratorID, problemID,
	).Scan(&genCode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Generator not found"})
		return
	}

	// Fetch problem limits
	var timeLimitMs, memoryLimitMb int
	_ = h.DB.QueryRow(ctx,
		`SELECT time_limit_ms, memory_limit_mb FROM app.problems WHERE id = $1`, problemID,
	).Scan(&timeLimitMs, &memoryLimitMb)

	// Run stress test iterations
	for i := 1; i <= req.Iterations; i++ {
		// Generate input
		args := req.ArgsTemplate
		if args != "" {
			args += " "
		}
		args += strconv.Itoa(i)

		input := runGenerator(genCode, args, timeLimitMs, memoryLimitMb)
		if input == "" {
			c.JSON(http.StatusOK, StressTestResponse{
				Success:       false,
				IterationsRun: i,
				Error:         "Generator failed at iteration " + strconv.Itoa(i),
			})
			return
		}

		mainOutput := runSolution(mainCode, input, timeLimitMs, memoryLimitMb)
		bruteOutput := runSolution(bruteCode, input, timeLimitMs, memoryLimitMb)

		// Compare outputs (token-by-token)
		if normalizeOutput(mainOutput) != normalizeOutput(bruteOutput) {
			failedAt := i
			userID, _ := c.Get("userID")
			uid := userID.(int)
			h.logAudit(uid, "stress_test.mismatch", "problem", problemID, map[string]interface{}{
				"iteration":         i,
				"main_solution_id":  req.MainSolutionID,
				"brute_solution_id": req.BruteSolutionID,
			}, c.ClientIP())

			c.JSON(http.StatusOK, StressTestResponse{
				Success:           false,
				IterationsRun:     i,
				FailedAtIteration: &failedAt,
				FailedInput:       input,
				MainOutput:        mainOutput,
				BruteOutput:       bruteOutput,
			})
			return
		}
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	h.logAudit(uid, "stress_test.pass", "problem", problemID, map[string]interface{}{
		"iterations": req.Iterations,
	}, c.ClientIP())

	c.JSON(http.StatusOK, StressTestResponse{
		Success:       true,
		IterationsRun: req.Iterations,
	})
}

// normalizeOutput normalizes whitespace for comparison.
func normalizeOutput(s string) string {
	// Split by whitespace, rejoin with single spaces
	fields := splitFields(s)
	result := ""
	for i, f := range fields {
		if i > 0 {
			result += " "
		}
		result += f
	}
	return result
}

// splitFields splits string by any whitespace.
func splitFields(s string) []string {
	fields := make([]string, 0)
	current := ""
	for _, ch := range s {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			if current != "" {
				fields = append(fields, current)
				current = ""
			}
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		fields = append(fields, current)
	}
	return fields
}

// Ensure the time import is used via a dummy reference
var _ = time.Now
