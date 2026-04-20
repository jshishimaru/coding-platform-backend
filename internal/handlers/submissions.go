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
	ID           int        `json:"id"`
	ProblemID    int        `json:"problem_id"`
	ProblemSlug  string     `json:"problem_slug,omitempty"`
	ProblemTitle string     `json:"problem_title,omitempty"`
	ProblemType  string     `json:"problem_type,omitempty"`
	Status       string     `json:"status"`
	Language     string     `json:"language"`
	RuntimeMs    *int       `json:"runtime_ms,omitempty"`
	MemoryKb     *int       `json:"memory_kb,omitempty"`
	PassedCount  int        `json:"passed_count"`
	TotalCount   int        `json:"total_count"`
	SubmittedAt  time.Time  `json:"submitted_at"`
	ContestID    *int       `json:"contest_id,omitempty"`
	ContestTitle string     `json:"contest_title,omitempty"`
	ManualScore  *int       `json:"manual_score,omitempty"`
	Feedback     string     `json:"feedback,omitempty"`
	IsLocked     bool       `json:"is_locked"`
	GradedAt     *time.Time `json:"graded_at,omitempty"`
	GraderName   string     `json:"grader_name,omitempty"`
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
	var checkerCode, problemType string
	err := h.DB.QueryRow(context.Background(),
		`SELECT id, time_limit_ms, memory_limit_mb, checker_code, problem_type
		 FROM app.problems WHERE slug = $1`, req.ProblemSlug,
	).Scan(&problemID, &timeLimitMs, &memoryLimitMb, &checkerCode, &problemType)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Question not found"})
		return
	}

	// Subjective problems: skip judging, accept as pending_review
	if problemType == "subjective" {
		var subID int
		var submittedAt time.Time
		err := h.DB.QueryRow(context.Background(),
			`INSERT INTO app.submissions (user_id, problem_id, language, source_code,
			                              status, passed_count, total_count)
			 VALUES ($1, $2, $3, $4, 'pending_review', 0, 0)
			 RETURNING id, submitted_at`,
			userID, problemID, req.Language, req.Code,
		).Scan(&subID, &submittedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save submission"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"submission": SubmissionResponse{
				ID:          subID,
				ProblemID:   problemID,
				ProblemSlug: req.ProblemSlug,
				Status:      "pending_review",
				Language:    req.Language,
				SubmittedAt: submittedAt,
			},
			"message": "Submission received and awaiting manual review.",
		})
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

	userID, _ := c.Get("userID")
	uid, _ := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	var s SubmissionResponse
	var resultJSON []byte
	var ownerID int
	var sourceCode string
	var graderName *string
	err = h.DB.QueryRow(context.Background(),
		`SELECT s.id, s.user_id, s.problem_id, p.slug, p.title, p.problem_type, s.status, s.language,
		        s.runtime_ms, s.memory_kb, COALESCE(s.passed_count,0),
		        COALESCE(s.total_count,0), s.submitted_at, s.result_details,
		        s.contest_id, COALESCE(c.title, ''), s.manual_score, s.feedback, s.is_locked,
		        s.graded_at, gu.username, s.source_code
		 FROM app.submissions s
		 JOIN app.problems p ON p.id = s.problem_id
		 LEFT JOIN app.contests c ON c.id = s.contest_id
		 LEFT JOIN app.users gu ON gu.id = s.graded_by
		 WHERE s.id = $1`, submissionID,
	).Scan(&s.ID, &ownerID, &s.ProblemID, &s.ProblemSlug, &s.ProblemTitle, &s.ProblemType,
		&s.Status, &s.Language,
		&s.RuntimeMs, &s.MemoryKb, &s.PassedCount, &s.TotalCount,
		&s.SubmittedAt, &resultJSON,
		&s.ContestID, &s.ContestTitle, &s.ManualScore, &s.Feedback, &s.IsLocked,
		&s.GradedAt, &graderName, &sourceCode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Submission not found"})
		return
	}
	if graderName != nil {
		s.GraderName = *graderName
	}

	// Only the owner or a site admin may view the submission detail
	if ownerID != uid && roleStr != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Forbidden"})
		return
	}

	response := gin.H{"submission": s, "source_code": sourceCode}
	if resultJSON != nil {
		var result sandbox.JudgeResult
		if json.Unmarshal(resultJSON, &result) == nil {
			response["result"] = result
		}
	}

	c.JSON(http.StatusOK, response)
}

// ListMySubmissions returns the authenticated user's submission history.
// Filters: ?contest_id=<n>&problem_slug=<s>&status=<s>&page=<n>
func (h *Handler) ListMySubmissions(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid, _ := userID.(int)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	limit := 25
	offset := (page - 1) * limit

	args := []interface{}{uid}
	query := `SELECT s.id, s.problem_id, p.slug, p.title, p.problem_type, s.status, s.language,
	                 s.runtime_ms, s.memory_kb, COALESCE(s.passed_count,0),
	                 COALESCE(s.total_count,0), s.submitted_at,
	                 s.contest_id, COALESCE(c.title, ''), s.manual_score, s.feedback, s.is_locked
	          FROM app.submissions s
	          JOIN app.problems p ON p.id = s.problem_id
	          LEFT JOIN app.contests c ON c.id = s.contest_id
	          WHERE s.user_id = $1`
	countQuery := `SELECT COUNT(*) FROM app.submissions s JOIN app.problems p ON p.id = s.problem_id WHERE s.user_id = $1`
	countArgs := []interface{}{uid}
	argIdx := 2

	if contestIDStr := c.Query("contest_id"); contestIDStr != "" {
		if cid, err := strconv.Atoi(contestIDStr); err == nil {
			clause := " AND s.contest_id = $" + strconv.Itoa(argIdx)
			query += clause
			countQuery += clause
			args = append(args, cid)
			countArgs = append(countArgs, cid)
			argIdx++
		}
	}
	if slug := c.Query("problem_slug"); slug != "" {
		clause := " AND p.slug = $" + strconv.Itoa(argIdx)
		query += clause
		countQuery += clause
		args = append(args, slug)
		countArgs = append(countArgs, slug)
		argIdx++
	}
	if status := c.Query("status"); status != "" {
		clause := " AND s.status = $" + strconv.Itoa(argIdx)
		query += clause
		countQuery += clause
		args = append(args, status)
		countArgs = append(countArgs, status)
		argIdx++
	}

	var total int
	_ = h.DB.QueryRow(context.Background(), countQuery, countArgs...).Scan(&total)

	query += " ORDER BY s.submitted_at DESC LIMIT $" + strconv.Itoa(argIdx) +
		" OFFSET $" + strconv.Itoa(argIdx+1)
	args = append(args, limit, offset)

	rows, err := h.DB.Query(context.Background(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	submissions := make([]SubmissionResponse, 0)
	for rows.Next() {
		var s SubmissionResponse
		if err := rows.Scan(&s.ID, &s.ProblemID, &s.ProblemSlug, &s.ProblemTitle, &s.ProblemType,
			&s.Status, &s.Language,
			&s.RuntimeMs, &s.MemoryKb, &s.PassedCount, &s.TotalCount, &s.SubmittedAt,
			&s.ContestID, &s.ContestTitle, &s.ManualScore, &s.Feedback, &s.IsLocked); err != nil {
			continue
		}
		submissions = append(submissions, s)
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  submissions,
		"total": total,
		"page":  page,
		"pages": (total + limit - 1) / limit,
	})
}

func (h *Handler) GetUserSubmissions(c *gin.Context) {
	userID, _ := c.Get("userID")

	// Return the best status per problem for this user.
	// "accepted" beats any other status, which all count as "attempted".
	rows, err := h.DB.Query(context.Background(),
		`SELECT p.id, p.slug, p.title,
		        CASE WHEN bool_or(s.status = 'accepted') THEN 'solved'
		             ELSE 'attempted' END AS best_status
		 FROM app.submissions s
		 JOIN app.problems p ON p.id = s.problem_id
		 WHERE s.user_id = $1
		 GROUP BY p.id, p.slug, p.title
		 ORDER BY p.id`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	type ProblemStatus struct {
		ProblemID int    `json:"problem_id"`
		Slug      string `json:"slug"`
		Title     string `json:"title"`
		Status    string `json:"status"` // "solved" or "attempted"
	}

	statuses := make([]ProblemStatus, 0)
	for rows.Next() {
		var ps ProblemStatus
		if err := rows.Scan(&ps.ProblemID, &ps.Slug, &ps.Title, &ps.Status); err != nil {
			continue
		}
		statuses = append(statuses, ps)
	}

	c.JSON(http.StatusOK, gin.H{"statuses": statuses})
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
