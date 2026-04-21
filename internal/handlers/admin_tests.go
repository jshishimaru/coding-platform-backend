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
	ExpectedOutput string `json:"expected_output"` // optional — can be filled in later via a solution run
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
	// Optional: if omitted, the active generator for the problem is used.
	// We intentionally don't mark this required so the admin UI can just say
	// "Generate" without forcing the user to pick one each time.
	GeneratorID              int    `json:"generator_id"`
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
	TestID     int    `json:"test_id"`
	OrderIndex int    `json:"order_index"` // 1-based display number
	Error      string `json:"error"`
	Input      string `json:"input"` // truncated for display
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
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 500 {
		limit = 200
	}
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

// AdminBulkDeleteTests deletes multiple test cases in one request.
// Body: { "ids": [1, 2, 3] }
func (h *Handler) AdminBulkDeleteTests(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	var req struct {
		IDs []int `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids array is required"})
		return
	}

	ctx := context.Background()
	// Build a parameterised IN clause scoped to this problem so we can't
	// accidentally delete tests belonging to another problem.
	args := make([]interface{}, 0, len(req.IDs)+1)
	args = append(args, problemID)
	placeholders := make([]string, len(req.IDs))
	for i, id := range req.IDs {
		args = append(args, id)
		placeholders[i] = "$" + strconv.Itoa(i+2)
	}
	query := "DELETE FROM app.test_cases WHERE problem_id = $1 AND id IN (" +
		joinStrings(placeholders, ",") + ")"

	tag, err := h.DB.Exec(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete test cases"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	h.logAudit(uid, "test.bulk_delete", "problem", problemID, map[string]interface{}{
		"ids": req.IDs, "deleted": tag.RowsAffected(),
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"deleted": tag.RowsAffected()})
}

// AdminBulkPatchTests updates a shared field across multiple test cases.
// Currently supported fields: is_sample (bool).
// Body: { "ids": [1,2,3], "is_sample": true }
func (h *Handler) AdminBulkPatchTests(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	var req struct {
		IDs      []int `json:"ids" binding:"required"`
		IsSample *bool `json:"is_sample"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids array is required"})
		return
	}
	if req.IsSample == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update (supported: is_sample)"})
		return
	}

	ctx := context.Background()
	args := make([]interface{}, 0, len(req.IDs)+2)
	args = append(args, *req.IsSample, problemID)
	placeholders := make([]string, len(req.IDs))
	for i, id := range req.IDs {
		args = append(args, id)
		placeholders[i] = "$" + strconv.Itoa(i+3)
	}
	query := "UPDATE app.test_cases SET is_sample = $1 WHERE problem_id = $2 AND id IN (" +
		joinStrings(placeholders, ",") + ")"

	tag, err := h.DB.Exec(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update test cases"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	h.logAudit(uid, "test.bulk_patch", "problem", problemID, map[string]interface{}{
		"ids": req.IDs, "is_sample": *req.IsSample, "updated": tag.RowsAffected(),
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"updated": tag.RowsAffected()})
}

// joinStrings is a tiny helper (strings.Join equivalent for slices).
func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
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

	if req.Count < 1 || req.Count > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Count must be between 1 and 1000"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	ctx := context.Background()

	// Resolve generator: explicit id from the request wins, otherwise fall
	// back to the active generator for this problem. This matches the UX of
	// the validators/checkers endpoints which also rely on the "active" row.
	var genCode string
	if req.GeneratorID > 0 {
		err = h.DB.QueryRow(ctx,
			`SELECT source_code FROM app.problem_generators WHERE id = $1 AND problem_id = $2`,
			req.GeneratorID, problemID,
		).Scan(&genCode)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Generator not found"})
			return
		}
	} else {
		err = h.DB.QueryRow(ctx,
			`SELECT id, source_code FROM app.problem_generators
			  WHERE problem_id = $1 AND is_active = TRUE
			  LIMIT 1`,
			problemID,
		).Scan(&req.GeneratorID, &genCode)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "No active generator. Create a generator and mark it Active, or pass generator_id explicitly.",
			})
			return
		}
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
	if memoryLimitMb <= 0 {
		memoryLimitMb = 256
	}

	genCfg := &sandbox.Config{
		MaxTimeSec:     maxInt(timeLimitMs/1000, 10),
		MaxMemoryMB:    memoryLimitMb,
		MaxOutputBytes: 1 * 1024 * 1024,
	}
	solnCfg := &sandbox.Config{
		MaxTimeSec:     maxInt(timeLimitMs/1000, 5),
		MaxMemoryMB:    memoryLimitMb,
		MaxOutputBytes: 1 * 1024 * 1024,
	}

	// Smoke-test seed=1 before creating the batch record.
	// Each seed compiles a fresh binary with -DSEED=N injected so generators
	// using srand(SEED), srand(atoi(argv[1])), or cin >> seed all work.
	smokeResult := sandbox.RunGeneratorWithSeed(genCode, req.Args, 1, genCfg)
	if smokeResult.Status != "success" || smokeResult.Stdout == "" {
		errMsg := smokeResult.Stderr
		if errMsg == "" {
			errMsg = "Generator produced no output for seed 1"
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "Generator smoke-test failed: " + errMsg})
		return
	}

	// Optionally compile solution once (it's deterministic, so compile-once is fine).
	var solnDir, solnBin string
	if solnCode != "" {
		var solnErr *sandbox.Result
		solnDir, solnBin, _, solnErr = sandbox.CompileOnly(solnCode)
		if solnErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Solution compilation failed: " + solnErr.Stderr})
			return
		}
		defer sandbox.CleanupDir(solnDir)
	}

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
		seed := i + 1 // 1-indexed seeds

		// Compile with -DSEED=N so srand(SEED) generators work correctly.
		// argv[1] and stdin are also set to the seed string (see RunGeneratorWithSeed).
		genResult := sandbox.RunGeneratorWithSeed(genCode, req.Args, seed, genCfg)
		if genResult.Status != "success" || genResult.Stdout == "" {
			errMsg := genResult.Stderr
			if errMsg == "" {
				errMsg = "Generator produced no output for seed " + strconv.Itoa(seed)
			}
			results = append(results, genTestResult{Error: errMsg})
			continue
		}
		input := genResult.Stdout

		// Optionally run the pre-compiled solution to produce expected output.
		expectedOutput := ""
		if solnCode != "" && solnDir != "" {
			solnResult := sandbox.RunCompiledWithStdin(solnDir, solnBin, input, solnCfg)
			if solnResult.Status == "success" {
				expectedOutput = solnResult.Stdout
			}
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

	// Update batch count to reflect only successfully created tests.
	saved := 0
	for _, r := range results {
		if r.TestID > 0 {
			saved++
		}
	}
	_, _ = h.DB.Exec(ctx,
		`UPDATE app.generated_test_batches SET test_count = $1 WHERE id = $2`,
		saved, batchID,
	)

	h.logAudit(uid, "test.generate", "problem", problemID, map[string]interface{}{
		"batch_id": batchID, "count": saved, "generator_id": req.GeneratorID,
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{
		"batch_id": batchID,
		"results":  results,
		"count":    saved,
	})
}

// AdminValidateTests compiles the active validator once and runs it against
// every test case for the problem. Returns a per-test failure list.
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active validator found. Create a validator and mark it Active."})
		return
	}

	// Compile the validator once — reuse the binary for all test cases.
	// Validators are deterministic; compiling N times is wasteful.
	validatorDir, validatorBin, _, compErr := sandbox.CompileOnly(validatorCode)
	if compErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Validator compilation failed: " + compErr.Stderr})
		return
	}
	defer sandbox.CleanupDir(validatorDir)

	// Fetch all test cases with their display order
	rows, err := h.DB.Query(ctx,
		`SELECT id, input, order_index
		   FROM app.test_cases
		  WHERE problem_id = $1
		  ORDER BY order_index, id`, problemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	type testRow struct {
		ID         int
		Input      string
		OrderIndex int
	}
	tests := make([]testRow, 0)
	for rows.Next() {
		var t testRow
		if err := rows.Scan(&t.ID, &t.Input, &t.OrderIndex); err == nil {
			tests = append(tests, t)
		}
	}

	// Fetch problem limits for the validator timeout / memory cap
	var timeLimitMs, memoryLimitMb int
	_ = h.DB.QueryRow(ctx,
		`SELECT time_limit_ms, memory_limit_mb FROM app.problems WHERE id = $1`, problemID,
	).Scan(&timeLimitMs, &memoryLimitMb)
	if memoryLimitMb <= 0 {
		memoryLimitMb = 256
	}

	valCfg := &sandbox.Config{
		MaxTimeSec:     maxInt(timeLimitMs/1000, 10),
		MaxMemoryMB:    memoryLimitMb,
		MaxOutputBytes: 512 * 1024,
	}

	failures := make([]ValidationError, 0)
	passed := 0
	displayIdx := 0

	for _, t := range tests {
		displayIdx++
		errMsg := runValidatorCompiled(validatorDir, validatorBin, t.Input, valCfg)
		if errMsg != "" {
			failures = append(failures, ValidationError{
				TestID:     t.ID,
				OrderIndex: displayIdx,
				Error:      errMsg,
				Input:      truncate(t.Input, 300),
			})
		} else {
			passed++
		}
	}

	// Mark batches as validated when everything passes
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

// AdminRunSolutionOnTest runs a specific solution against a specific test case
// and updates that test case's expected_output with the result.
// Body: { "solution_id": 3 }  — omit to use the main-tagged solution.
func (h *Handler) AdminRunSolutionOnTest(c *gin.Context) {
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

	var req struct {
		SolutionID *int `json:"solution_id"`
	}
	_ = c.ShouldBindJSON(&req) // body is fully optional

	ctx := context.Background()

	// Fetch the test case input
	var testInput string
	err = h.DB.QueryRow(ctx,
		`SELECT input FROM app.test_cases WHERE id = $1 AND problem_id = $2`,
		testID, problemID,
	).Scan(&testInput)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Test case not found"})
		return
	}

	// Resolve solution: explicit id > tag='main' > any
	var solnCode string
	if req.SolutionID != nil {
		err = h.DB.QueryRow(ctx,
			`SELECT source_code FROM app.problem_solutions WHERE id = $1 AND problem_id = $2`,
			*req.SolutionID, problemID,
		).Scan(&solnCode)
	} else {
		err = h.DB.QueryRow(ctx,
			`SELECT source_code FROM app.problem_solutions
			  WHERE problem_id = $1 ORDER BY (tag='main') DESC, id ASC LIMIT 1`,
			problemID,
		).Scan(&solnCode)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No solution found for this problem. Add one in the Solutions tab."})
		return
	}

	// Fetch problem limits
	var timeLimitMs, memoryLimitMb int
	_ = h.DB.QueryRow(ctx,
		`SELECT time_limit_ms, memory_limit_mb FROM app.problems WHERE id = $1`, problemID,
	).Scan(&timeLimitMs, &memoryLimitMb)
	if memoryLimitMb <= 0 {
		memoryLimitMb = 256
	}

	solnCfg := &sandbox.Config{
		MaxTimeSec:     maxInt(timeLimitMs/1000, 5),
		MaxMemoryMB:    memoryLimitMb,
		MaxOutputBytes: 1 * 1024 * 1024,
	}

	result := sandbox.RunCpp(solnCode, testInput, solnCfg)

	if result.Status == "compilation_error" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Solution compilation failed: " + result.Stderr})
		return
	}
	if result.Status != "success" {
		c.JSON(http.StatusOK, gin.H{
			"success":         false,
			"status":          result.Status,
			"error":           result.Stderr,
			"expected_output": "",
		})
		return
	}

	// Persist the new expected output
	_, err = h.DB.Exec(ctx,
		`UPDATE app.test_cases SET expected_output = $1 WHERE id = $2 AND problem_id = $3`,
		result.Stdout, testID, problemID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update expected output"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	h.logAudit(uid, "test.run_solution", "test_case", testID, map[string]interface{}{
		"problem_id": problemID,
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"status":          result.Status,
		"expected_output": result.Stdout,
		"time_ms":         result.TimeTakenMs,
	})
}

// ──────────────────────────────────────────────────────────
// Sandbox helpers for test generation / validation
// ──────────────────────────────────────────────────────────

// runGenerator compiles and runs the generator with args as CLI argv (not stdin).
// testlib.h generators receive their seed via argv[1], so this is correct.
func runGenerator(code, args string, timeLimitMs, memoryLimitMb int) string {
	cfg := &sandbox.Config{
		MaxTimeSec:     maxInt(timeLimitMs/1000, 10),
		MaxMemoryMB:    maxInt(memoryLimitMb, 256),
		MaxOutputBytes: 1 * 1024 * 1024,
	}
	result := sandbox.RunCppWithArgs(code, args, cfg)
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

// runValidator compiles and runs the validator from source each time.
// Kept for one-off calls; prefer runValidatorCompiled for bulk validation.
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

// runValidatorCompiled runs a pre-compiled validator binary against the given
// input. Returns the empty string on success, or a descriptive error message.
func runValidatorCompiled(tmpDir, binPath, input string, cfg *sandbox.Config) string {
	result := sandbox.RunCompiledWithStdin(tmpDir, binPath, input, cfg)
	if result.Status != "success" || result.ExitCode != 0 {
		// Prefer stderr (testlib.h writes its assertion messages there)
		errMsg := result.Stderr
		if errMsg == "" {
			errMsg = "Validator returned non-zero exit code " + strconv.Itoa(result.ExitCode)
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
