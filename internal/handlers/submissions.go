package handlers

import (
"context"
"encoding/json"
"net/http"
"strconv"
"time"

"github.com/gin-gonic/gin"

"github.com/coding-platform/backend/internal/sandbox"
)

// ---------- Request / response types ----------

type CreateSubmissionRequest struct {
	ProblemSlug string `json:"problem_slug" binding:"required"`
	Code        string `json:"code" binding:"required"`
	Language    string `json:"language" binding:"required"`
}

type SubmissionResponse struct {
	ID          int       `json:"id"`
	ProblemID   int       `json:"problem_id"`
	ProblemSlug string    `json:"problem_slug,omitempty"`
	Status      string    `json:"status"`
	Language    string    `json:"language"`
	RuntimeMs   *int      `json:"runtime_ms,omitempty"`
	MemoryKb    *int      `json:"memory_kb,omitempty"`
	PassedCount int       `json:"passed_count"`
	TotalCount  int       `json:"total_count"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// ---------- Handlers ----------

func (h *Handler) SubmissionsHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "module": "submissions"})
}

func (h *Handler) CreateSubmission(c *gin.Context) {
	var req CreateSubmissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Language != "cpp" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only C++ is supported currently"})
		return
	}

	userID, _ := c.Get("userID")

	// Fetch problem
	var problemID, timeLimitMs, memoryLimitMb int
	var checkerCode string
	err := h.DB.QueryRow(context.Background(),
		`SELECT id, time_limit_ms, memory_limit_mb, checker_code
		 FROM app.problems WHERE slug = $1`, req.ProblemSlug,
	).Scan(&problemID, &timeLimitMs, &memoryLimitMb, &checkerCode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Question not found"})
		return
	}

	// Fetch ALL test cases (sample + hidden)
	rows, err := h.DB.Query(context.Background(),
		`SELECT id, input, expected_output, is_sample
		 FROM app.test_cases WHERE problem_id = $1 ORDER BY id`, problemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch test cases"})
		return
	}
	defer rows.Close()

	var testCases []sandbox.TestCaseInput
	for rows.Next() {
		var tc sandbox.TestCaseInput
		if err := rows.Scan(&tc.ID, &tc.Input, &tc.ExpectedOutput, &tc.IsSample); err != nil {
			continue
		}
		testCases = append(testCases, tc)
	}

	if len(testCases) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No test cases found"})
		return
	}

	// Run judge
	result := sandbox.Judge(&sandbox.JudgeRequest{
		Code:          req.Code,
		Language:      req.Language,
		TestCases:     testCases,
		CheckerCode:   checkerCode,
		TimeLimitMs:   timeLimitMs,
		MemoryLimitMB: memoryLimitMb,
	})

	// Compute max runtime and memory for the submission record
	var runtimeMs, memoryKb *int
	if result.MaxTimeMs > 0 {
		v := int(result.MaxTimeMs)
		runtimeMs = &v
	}
	if result.MaxMemoryKB > 0 {
		v := int(result.MaxMemoryKB)
		memoryKb = &v
	}

	// Marshal result details
	resultJSON, _ := json.Marshal(result)

	// Insert submission
	var submissionID int
	var submittedAt time.Time
	err = h.DB.QueryRow(context.Background(),
		`INSERT INTO app.submissions (user_id, problem_id, language, source_code, status, runtime_ms, memory_kb, passed_count, total_count, result_details)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id, submitted_at`,
		userID, problemID, req.Language, req.Code, result.Status,
		runtimeMs, memoryKb, result.PassedCount, result.TotalCount, resultJSON,
	).Scan(&submissionID, &submittedAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save submission: " + err.Error()})
		return
	}

	// Strip stdout/stderr from hidden test cases before returning
	for i := range result.TestCaseResults {
		if !result.TestCaseResults[i].IsSample {
			result.TestCaseResults[i].Stdout = ""
			result.TestCaseResults[i].Stderr = ""
		}
	}

	c.JSON(http.StatusOK, gin.H{
"submission": SubmissionResponse{
ID:          submissionID,
ProblemID:   problemID,
ProblemSlug: req.ProblemSlug,
Status:      result.Status,
Language:    req.Language,
RuntimeMs:   runtimeMs,
MemoryKb:    memoryKb,
PassedCount: result.PassedCount,
TotalCount:  result.TotalCount,
SubmittedAt: submittedAt,
},
"result": result,
})
}

func (h *Handler) GetSubmission(c *gin.Context) {
	id := c.Param("id")
	submissionID, err := strconv.Atoi(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid submission ID"})
		return
	}

	var s SubmissionResponse
	var resultJSON []byte
	err = h.DB.QueryRow(context.Background(),
		`SELECT s.id, s.problem_id, p.slug, s.status, s.language,
		        s.runtime_ms, s.memory_kb, COALESCE(s.passed_count,0),
		        COALESCE(s.total_count,0), s.submitted_at, s.result_details
		 FROM app.submissions s
		 JOIN app.problems p ON p.id = s.problem_id
		 WHERE s.id = $1`, submissionID,
	).Scan(&s.ID, &s.ProblemID, &s.ProblemSlug, &s.Status, &s.Language,
		&s.RuntimeMs, &s.MemoryKb, &s.PassedCount, &s.TotalCount,
		&s.SubmittedAt, &resultJSON)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Submission not found"})
		return
	}

	response := gin.H{"submission": s}
	if resultJSON != nil {
		var result sandbox.JudgeResult
		if json.Unmarshal(resultJSON, &result) == nil {
			response["result"] = result
		}
	}

	c.JSON(http.StatusOK, response)
}

func (h *Handler) GetUserSubmissions(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"submissions": []interface{}{}})
}

func (h *Handler) GetQuestionSubmissions(c *gin.Context) {
	slug := c.Param("slug")
	userID, _ := c.Get("userID")

	rows, err := h.DB.Query(context.Background(),
		`SELECT s.id, s.problem_id, p.slug, s.status, s.language,
		        s.runtime_ms, s.memory_kb, COALESCE(s.passed_count,0),
		        COALESCE(s.total_count,0), s.submitted_at
		 FROM app.submissions s
		 JOIN app.problems p ON p.id = s.problem_id
		 WHERE p.slug = $1 AND s.user_id = $2
		 ORDER BY s.submitted_at DESC
		 LIMIT 20`, slug, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	submissions := make([]SubmissionResponse, 0)
	for rows.Next() {
		var s SubmissionResponse
		if err := rows.Scan(&s.ID, &s.ProblemID, &s.ProblemSlug, &s.Status, &s.Language,
			&s.RuntimeMs, &s.MemoryKb, &s.PassedCount, &s.TotalCount, &s.SubmittedAt); err != nil {
			continue
		}
		submissions = append(submissions, s)
	}

	c.JSON(http.StatusOK, gin.H{"submissions": submissions})
}
