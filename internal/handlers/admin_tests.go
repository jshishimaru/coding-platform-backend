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

type AdminTestCaseInfo struct {
	ID               int       `json:"id"`
	ProblemID        int       `json:"problem_id"`
	Input            string    `json:"input"`
	ExpectedOutput   string    `json:"expected_output"`
	IsSample         bool      `json:"is_sample"`
	OrderIndex       int       `json:"order_index"`
	GeneratorBatchID *int      `json:"generator_batch_id"`
	CreatedBy        *int      `json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
}

type AdminCreateTestRequest struct {
	Input          string `json:"input" binding:"required"`
	ExpectedOutput string `json:"expected_output" binding:"required"`
	IsSample       bool   `json:"is_sample"`
	OrderIndex     int    `json:"order_index"`
}

type AdminBulkTestRequest struct {
	Tests []AdminCreateTestRequest `json:"tests" binding:"required"`
}

type AdminUpdateTestRequest struct {
	Input          *string `json:"input"`
	ExpectedOutput *string `json:"expected_output"`
	IsSample       *bool   `json:"is_sample"`
	OrderIndex     *int    `json:"order_index"`
}

type AdminReorderTestsRequest struct {
	Order []TestOrder `json:"order" binding:"required"`
}

type TestOrder struct {
	TestID     int `json:"test_id"`
	OrderIndex int `json:"order_index"`
}

type AdminGenerateTestsRequest struct {
	GeneratorID              int    `json:"generator_id" binding:"required"`
	Args                     string `json:"args"`
	Count                    int    `json:"count" binding:"required"`
	GenerateOutputWithSolnID *int   `json:"generate_output_with_solution_id"`
}

type AdminValidateTestsResponse struct {
	Valid    bool              `json:"valid"`
	Total    int               `json:"total"`
	Passed   int               `json:"passed"`
	Failures []ValidationError `json:"failures"`
}

type ValidationError struct {
	TestID int    `json:"test_id"`
	Error  string `json:"error"`
}

// ──────────────────────────────────────────────────────────
// Handlers
// ──────────────────────────────────────────────────────────

// AdminListTests lists all test cases for a problem.
func (h *Handler) AdminListTests(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	limit := 50
	offset := (page - 1) * limit

	ctx := context.Background()

	var total int
	_ = h.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM app.test_cases WHERE problem_id = $1`, problemID,
	).Scan(&total)

	rows, err := h.DB.Query(ctx,
		`SELECT id, problem_id, input, expected_output, is_sample, order_index,
		        generator_batch_id, created_by, created_at
		 FROM app.test_cases
		 WHERE problem_id = $1
		 ORDER BY order_index, id
		 LIMIT $2 OFFSET $3`, problemID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	tests := make([]AdminTestCaseInfo, 0)
	for rows.Next() {
		var t AdminTestCaseInfo
		if err := rows.Scan(&t.ID, &t.ProblemID, &t.Input, &t.ExpectedOutput,
			&t.IsSample, &t.OrderIndex, &t.GeneratorBatchID, &t.CreatedBy, &t.CreatedAt); err != nil {
			continue
		}
		tests = append(tests, t)
	}

	c.JSON(http.StatusOK, gin.H{
		"tests": tests,
		"total": total,
		"page":  page,
		"pages": (total + limit - 1) / limit,
	})
}

// AdminCreateTest creates a single manual test case.
func (h *Handler) AdminCreateTest(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	var req AdminCreateTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	ctx := context.Background()

	// Auto-assign order_index if not provided
	if req.OrderIndex == 0 {
		var maxOrder int
		_ = h.DB.QueryRow(ctx,
			`SELECT COALESCE(MAX(order_index), 0) FROM app.test_cases WHERE problem_id = $1`, problemID,
		).Scan(&maxOrder)
		req.OrderIndex = maxOrder + 1
	}

	var testID int
	err = h.DB.QueryRow(ctx,
		`INSERT INTO app.test_cases (problem_id, input, expected_output, is_sample, order_index, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		problemID, req.Input, req.ExpectedOutput, req.IsSample, req.OrderIndex, uid,
	).Scan(&testID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create test case"})
		return
	}

	h.logAudit(uid, "test.create", "test_case", testID, map[string]interface{}{
		"problem_id": problemID, "is_sample": req.IsSample,
	}, c.ClientIP())

	c.JSON(http.StatusCreated, gin.H{"id": testID})
}

// AdminBulkCreateTests uploads multiple test cases at once.
func (h *Handler) AdminBulkCreateTests(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	var req AdminBulkTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.Tests) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No tests provided"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	ctx := context.Background()

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer tx.Rollback(ctx)

	// Get current max order_index
	var maxOrder int
	_ = tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(order_index), 0) FROM app.test_cases WHERE problem_id = $1`, problemID,
	).Scan(&maxOrder)

	ids := make([]int, 0, len(req.Tests))
	for i, t := range req.Tests {
		orderIdx := t.OrderIndex
		if orderIdx == 0 {
			orderIdx = maxOrder + i + 1
		}
		var testID int
		err := tx.QueryRow(ctx,
			`INSERT INTO app.test_cases (problem_id, input, expected_output, is_sample, order_index, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			problemID, t.Input, t.ExpectedOutput, t.IsSample, orderIdx, uid,
		).Scan(&testID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create test case"})
			return
		}
		ids = append(ids, testID)
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	h.logAudit(uid, "test.bulk_create", "problem", problemID, map[string]interface{}{
		"count": len(ids),
	}, c.ClientIP())

	c.JSON(http.StatusCreated, gin.H{"ids": ids, "count": len(ids)})
}

// AdminUpdateTest updates a single test case.
func (h *Handler) AdminUpdateTest(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}
	testID, err := strconv.Atoi(c.Param("testId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid test ID"})
		return
	}

	var req AdminUpdateTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()

	// Verify test belongs to problem
	var exists bool
	_ = h.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM app.test_cases WHERE id = $1 AND problem_id = $2)`,
		testID, problemID,
	).Scan(&exists)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Test case not found"})
		return
	}

	// Build update query dynamically
	updates := []string{}
	args := []interface{}{}
	argIdx := 1

	if req.Input != nil {
		updates = append(updates, "input = $"+strconv.Itoa(argIdx))
		args = append(args, *req.Input)
		argIdx++
	}
	if req.ExpectedOutput != nil {
		updates = append(updates, "expected_output = $"+strconv.Itoa(argIdx))
		args = append(args, *req.ExpectedOutput)
		argIdx++
	}
	if req.IsSample != nil {
		updates = append(updates, "is_sample = $"+strconv.Itoa(argIdx))
		args = append(args, *req.IsSample)
		argIdx++
	}
	if req.OrderIndex != nil {
		updates = append(updates, "order_index = $"+strconv.Itoa(argIdx))
		args = append(args, *req.OrderIndex)
		argIdx++
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update"})
		return
	}

	query := "UPDATE app.test_cases SET "
	for i, u := range updates {
		if i > 0 {
			query += ", "
		}
		query += u
	}
	query += " WHERE id = $" + strconv.Itoa(argIdx)
	args = append(args, testID)

	_, err = h.DB.Exec(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update test case"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Test case updated"})
}

// AdminDeleteTest deletes a single test case.
func (h *Handler) AdminDeleteTest(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}
	testID, err := strconv.Atoi(c.Param("testId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid test ID"})
		return
	}

	_, err = h.DB.Exec(context.Background(),
		`DELETE FROM app.test_cases WHERE id = $1 AND problem_id = $2`,
		testID, problemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete test case"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	h.logAudit(uid, "test.delete", "test_case", testID, map[string]interface{}{
		"problem_id": problemID,
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"message": "Test case deleted"})
}

// AdminReorderTests reorders test cases for a problem.
func (h *Handler) AdminReorderTests(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	var req AdminReorderTestsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer tx.Rollback(ctx)

	for _, o := range req.Order {
		_, err := tx.Exec(ctx,
			`UPDATE app.test_cases SET order_index = $1 WHERE id = $2 AND problem_id = $3`,
			o.OrderIndex, o.TestID, problemID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder"})
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Tests reordered"})
}

// AdminGenerateTests generates test cases using a generator + optional model solution.
func (h *Handler) AdminGenerateTests(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	var req AdminGenerateTestsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Count < 1 || req.Count > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Count must be between 1 and 100"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	ctx := context.Background()

	// Fetch generator source code
	var genCode string
	err = h.DB.QueryRow(ctx,
		`SELECT source_code FROM app.problem_generators WHERE id = $1 AND problem_id = $2`,
		req.GeneratorID, problemID,
	).Scan(&genCode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Generator not found"})
		return
	}

	// Optionally fetch model solution
	var solnCode string
	if req.GenerateOutputWithSolnID != nil {
		err = h.DB.QueryRow(ctx,
			`SELECT source_code FROM app.problem_solutions WHERE id = $1 AND problem_id = $2`,
			*req.GenerateOutputWithSolnID, problemID,
		).Scan(&solnCode)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Model solution not found"})
			return
		}
	}

	// Fetch problem limits
	var timeLimitMs, memoryLimitMb int
	_ = h.DB.QueryRow(ctx,
		`SELECT time_limit_ms, memory_limit_mb FROM app.problems WHERE id = $1`, problemID,
	).Scan(&timeLimitMs, &memoryLimitMb)

	// Create a batch record
	var batchID int
	err = h.DB.QueryRow(ctx,
		`INSERT INTO app.generated_test_batches (problem_id, generator_id, args, test_count, created_by)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		problemID, req.GeneratorID, req.Args, req.Count, uid,
	).Scan(&batchID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create batch"})
		return
	}

	// Compile generator (test run)
	testOutput := runGenerator(genCode, "1", timeLimitMs, memoryLimitMb)
	if testOutput == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Generator failed to compile or produce output"})
		return
	}

	// Get max order_index
	var maxOrder int
	_ = h.DB.QueryRow(ctx,
		`SELECT COALESCE(MAX(order_index), 0) FROM app.test_cases WHERE problem_id = $1`, problemID,
	).Scan(&maxOrder)

	type genTestResult struct {
		TestID int    `json:"test_id"`
		Input  string `json:"input"`
		Output string `json:"output"`
		Error  string `json:"error,omitempty"`
	}
	results := make([]genTestResult, 0, req.Count)

	for i := 0; i < req.Count; i++ {
		// Run generator with seed argument appended
		args := req.Args
		if args != "" {
			args += " "
		}
		args += strconv.Itoa(i + 1)

		input := runGenerator(genCode, args, timeLimitMs, memoryLimitMb)
		if input == "" {
			results = append(results, genTestResult{Error: "Generator produced empty output for seed " + strconv.Itoa(i+1)})
			continue
		}

		expectedOutput := ""
		if solnCode != "" {
			expectedOutput = runSolution(solnCode, input, timeLimitMs, memoryLimitMb)
		}

		orderIdx := maxOrder + i + 1
		var testID int
		err := h.DB.QueryRow(ctx,
			`INSERT INTO app.test_cases (problem_id, input, expected_output, is_sample, order_index, generator_batch_id, created_by)
			 VALUES ($1, $2, $3, FALSE, $4, $5, $6) RETURNING id`,
			problemID, input, expectedOutput, orderIdx, batchID, uid,
		).Scan(&testID)
		if err != nil {
			results = append(results, genTestResult{Error: "Failed to save test"})
			continue
		}
		results = append(results, genTestResult{TestID: testID, Input: truncate(input, 200), Output: truncate(expectedOutput, 200)})
	}

	// Update batch count
	_, _ = h.DB.Exec(ctx,
		`UPDATE app.generated_test_batches SET test_count = $1 WHERE id = $2`,
		len(results), batchID,
	)

	h.logAudit(uid, "test.generate", "problem", problemID, map[string]interface{}{
		"batch_id": batchID, "count": len(results), "generator_id": req.GeneratorID,
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{
		"batch_id": batchID,
		"results":  results,
		"count":    len(results),
	})
}

// AdminValidateTests runs the active validator against all test cases.
func (h *Handler) AdminValidateTests(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	ctx := context.Background()

	// Fetch active validator
	var validatorCode string
	err = h.DB.QueryRow(ctx,
		`SELECT source_code FROM app.problem_validators
		 WHERE problem_id = $1 AND is_active = TRUE LIMIT 1`, problemID,
	).Scan(&validatorCode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active validator found"})
		return
	}

	// Fetch all test cases
	rows, err := h.DB.Query(ctx,
		`SELECT id, input FROM app.test_cases WHERE problem_id = $1 ORDER BY order_index, id`, problemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	type testInput struct {
		ID    int
		Input string
	}
	tests := make([]testInput, 0)
	for rows.Next() {
		var t testInput
		if err := rows.Scan(&t.ID, &t.Input); err == nil {
			tests = append(tests, t)
		}
	}

	// Fetch problem limits
	var timeLimitMs, memoryLimitMb int
	_ = h.DB.QueryRow(ctx,
		`SELECT time_limit_ms, memory_limit_mb FROM app.problems WHERE id = $1`, problemID,
	).Scan(&timeLimitMs, &memoryLimitMb)

	failures := make([]ValidationError, 0)
	passed := 0

	for _, t := range tests {
		result := runValidator(validatorCode, t.Input, timeLimitMs, memoryLimitMb)
		if result != "" {
			failures = append(failures, ValidationError{TestID: t.ID, Error: result})
		} else {
			passed++
		}
	}

	// Update batch validation status if all pass
	if len(failures) == 0 {
		_, _ = h.DB.Exec(ctx,
			`UPDATE app.generated_test_batches SET validated = TRUE WHERE problem_id = $1`, problemID)
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	h.logAudit(uid, "test.validate", "problem", problemID, map[string]interface{}{
		"total": len(tests), "passed": passed, "failed": len(failures),
	}, c.ClientIP())

	c.JSON(http.StatusOK, AdminValidateTestsResponse{
		Valid:    len(failures) == 0,
		Total:    len(tests),
		Passed:   passed,
		Failures: failures,
	})
}

// ──────────────────────────────────────────────────────────
// Sandbox helpers for test generation / validation
// ──────────────────────────────────────────────────────────

// runGenerator compiles and runs the generator with given args as stdin.
func runGenerator(code, args string, timeLimitMs, memoryLimitMb int) string {
	cfg := &sandbox.Config{
		MaxTimeSec:     maxInt(timeLimitMs/1000, 10),
		MaxMemoryMB:    memoryLimitMb,
		MaxOutputBytes: 1 * 1024 * 1024,
	}
	result := sandbox.RunCpp(code, args, cfg)
	if result.Status != "success" {
		return ""
	}
	return result.Stdout
}

// runSolution runs a solution with given input.
func runSolution(code, input string, timeLimitMs, memoryLimitMb int) string {
	cfg := &sandbox.Config{
		MaxTimeSec:     maxInt(timeLimitMs/1000, 5),
		MaxMemoryMB:    memoryLimitMb,
		MaxOutputBytes: 1 * 1024 * 1024,
	}
	result := sandbox.RunCpp(code, input, cfg)
	if result.Status != "success" {
		return ""
	}
	return result.Stdout
}

// runValidator runs the validator with the test input.
// Returns empty string if valid, error message if invalid.
func runValidator(code, input string, timeLimitMs, memoryLimitMb int) string {
	cfg := &sandbox.Config{
		MaxTimeSec:     maxInt(timeLimitMs/1000, 10),
		MaxMemoryMB:    memoryLimitMb,
		MaxOutputBytes: 1 * 1024 * 1024,
	}
	result := sandbox.RunCpp(code, input, cfg)
	if result.Status != "success" || result.ExitCode != 0 {
		errMsg := result.Stderr
		if errMsg == "" {
			errMsg = "Validator returned non-zero exit code"
		}
		return errMsg
	}
	return ""
}

// truncate shortens a string to maxLen with ellipsis.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
