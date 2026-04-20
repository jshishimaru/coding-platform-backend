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

// ──────────────────────────────────────────────────────────────
// Types
// ──────────────────────────────────────────────────────────────

type AdminSubmissionSummary struct {
	ID          int        `json:"id"`
	UserID      int        `json:"user_id"`
	Username    string     `json:"username"`
	ProblemID   int        `json:"problem_id"`
	ProblemSlug string     `json:"problem_slug"`
	ProblemType string     `json:"problem_type"`
	ContestID   *int       `json:"contest_id,omitempty"`
	ContestName string     `json:"contest_name,omitempty"`
	Language    string     `json:"language"`
	Status      string     `json:"status"`
	PassedCount int        `json:"passed_count"`
	TotalCount  int        `json:"total_count"`
	ManualScore *int       `json:"manual_score,omitempty"`
	IsLocked    bool       `json:"is_locked"`
	HasFeedback bool       `json:"has_feedback"`
	SubmittedAt time.Time  `json:"submitted_at"`
	GradedAt    *time.Time `json:"graded_at,omitempty"`
}

type AdminSubmissionDetail struct {
	AdminSubmissionSummary
	SourceCode string          `json:"source_code"`
	Feedback   string          `json:"feedback"`
	GraderName string          `json:"grader_name,omitempty"`
	MaxPoints  int             `json:"max_points,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
}

type GradeSubmissionRequest struct {
	ManualScore *int    `json:"manual_score"`
	Feedback    *string `json:"feedback"`
	Status      *string `json:"status"`
	Lock        *bool   `json:"lock"`
}

type RunSubmissionRequest struct {
	Input string `json:"input"`
}

// ──────────────────────────────────────────────────────────────
// Handlers
// ──────────────────────────────────────────────────────────────

// AdminListSubmissions returns all submissions with rich filters:
// ?contest_id=&user_id=&problem_id=&status=&problem_type=&locked=&page=
func (h *Handler) AdminListSubmissions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	limit := 25
	offset := (page - 1) * limit

	args := []interface{}{}
	query := `SELECT s.id, s.user_id, u.username, s.problem_id, p.slug, p.problem_type,
	                 s.contest_id, COALESCE(c.title, ''), s.language, s.status,
	                 COALESCE(s.passed_count, 0), COALESCE(s.total_count, 0),
	                 s.manual_score, s.is_locked,
	                 (CASE WHEN COALESCE(s.feedback, '') <> '' THEN TRUE ELSE FALSE END),
	                 s.submitted_at, s.graded_at
	          FROM app.submissions s
	          JOIN app.users u    ON u.id = s.user_id
	          JOIN app.problems p ON p.id = s.problem_id
	          LEFT JOIN app.contests c ON c.id = s.contest_id
	          WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM app.submissions s
	               JOIN app.problems p ON p.id = s.problem_id
	               WHERE 1=1`
	countArgs := []interface{}{}
	argIdx := 1

	addFilter := func(clause string, vals ...interface{}) {
		query += clause
		countQuery += clause
		args = append(args, vals...)
		countArgs = append(countArgs, vals...)
		argIdx += len(vals)
	}

	if v := c.Query("contest_id"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			addFilter(" AND s.contest_id = $"+strconv.Itoa(argIdx), n)
		}
	}
	if v := c.Query("user_id"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			addFilter(" AND s.user_id = $"+strconv.Itoa(argIdx), n)
		}
	}
	if v := c.Query("problem_id"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			addFilter(" AND s.problem_id = $"+strconv.Itoa(argIdx), n)
		}
	}
	if v := c.Query("status"); v != "" {
		addFilter(" AND s.status = $"+strconv.Itoa(argIdx), v)
	}
	if v := c.Query("problem_type"); v != "" {
		addFilter(" AND p.problem_type = $"+strconv.Itoa(argIdx), v)
	}
	if v := c.Query("locked"); v == "true" || v == "false" {
		addFilter(" AND s.is_locked = $"+strconv.Itoa(argIdx), v == "true")
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

	submissions := make([]AdminSubmissionSummary, 0)
	for rows.Next() {
		var s AdminSubmissionSummary
		if err := rows.Scan(&s.ID, &s.UserID, &s.Username, &s.ProblemID, &s.ProblemSlug,
			&s.ProblemType, &s.ContestID, &s.ContestName, &s.Language, &s.Status,
			&s.PassedCount, &s.TotalCount, &s.ManualScore, &s.IsLocked, &s.HasFeedback,
			&s.SubmittedAt, &s.GradedAt); err == nil {
			submissions = append(submissions, s)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  submissions,
		"total": total,
		"page":  page,
		"pages": (total + limit - 1) / limit,
	})
}

// AdminGetSubmission returns full submission detail including source code.
func (h *Handler) AdminGetSubmission(c *gin.Context) {
	submissionID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid submission ID"})
		return
	}

	var sd AdminSubmissionDetail
	var grader *string
	var resultJSON []byte
	var maxPoints *int
	err = h.DB.QueryRow(context.Background(),
		`SELECT s.id, s.user_id, u.username, s.problem_id, p.slug, p.problem_type,
		        s.contest_id, COALESCE(c.title, ''), s.language, s.status,
		        COALESCE(s.passed_count, 0), COALESCE(s.total_count, 0),
		        s.manual_score, s.is_locked,
		        (CASE WHEN COALESCE(s.feedback, '') <> '' THEN TRUE ELSE FALSE END),
		        s.submitted_at, s.graded_at,
		        s.source_code, COALESCE(s.feedback, ''), gu.username,
		        (SELECT cp.max_points FROM app.contest_problems cp
		         WHERE cp.contest_id = s.contest_id AND cp.problem_id = s.problem_id
		         LIMIT 1),
		        s.result_details
		 FROM app.submissions s
		 JOIN app.users u    ON u.id = s.user_id
		 JOIN app.problems p ON p.id = s.problem_id
		 LEFT JOIN app.contests c ON c.id = s.contest_id
		 LEFT JOIN app.users    gu ON gu.id = s.graded_by
		 WHERE s.id = $1`, submissionID,
	).Scan(&sd.ID, &sd.UserID, &sd.Username, &sd.ProblemID, &sd.ProblemSlug,
		&sd.ProblemType, &sd.ContestID, &sd.ContestName, &sd.Language, &sd.Status,
		&sd.PassedCount, &sd.TotalCount, &sd.ManualScore, &sd.IsLocked, &sd.HasFeedback,
		&sd.SubmittedAt, &sd.GradedAt,
		&sd.SourceCode, &sd.Feedback, &grader, &maxPoints, &resultJSON)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Submission not found"})
		return
	}
	if grader != nil {
		sd.GraderName = *grader
	}
	if maxPoints != nil {
		sd.MaxPoints = *maxPoints
	}
	if resultJSON != nil {
		sd.Result = resultJSON
	}

	c.JSON(http.StatusOK, sd)
}

// AdminGradeSubmission lets an admin set manual_score / feedback / status / lock.
func (h *Handler) AdminGradeSubmission(c *gin.Context) {
	submissionID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid submission ID"})
		return
	}

	var req GradeSubmissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()
	userID, _ := c.Get("userID")
	uid := userID.(int)

	// Check if already locked (only unlock-then-regrade allowed)
	var isLocked bool
	var contestID *int
	var problemID, userOwner int
	var submittedAt time.Time
	err = h.DB.QueryRow(ctx,
		`SELECT is_locked, contest_id, problem_id, user_id, submitted_at
		 FROM app.submissions WHERE id = $1`, submissionID,
	).Scan(&isLocked, &contestID, &problemID, &userOwner, &submittedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Submission not found"})
		return
	}
	if isLocked && (req.ManualScore != nil || req.Status != nil) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Submission is locked. Unlock before regrading."})
		return
	}

	updates := []string{}
	args := []interface{}{}
	argIdx := 1
	if req.ManualScore != nil {
		updates = append(updates, "manual_score = $"+strconv.Itoa(argIdx))
		args = append(args, *req.ManualScore)
		argIdx++
	}
	if req.Feedback != nil {
		updates = append(updates, "feedback = $"+strconv.Itoa(argIdx))
		args = append(args, *req.Feedback)
		argIdx++
	}
	if req.Status != nil {
		if !isValidSubmissionStatus(*req.Status) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status"})
			return
		}
		updates = append(updates, "status = $"+strconv.Itoa(argIdx))
		args = append(args, *req.Status)
		argIdx++
	}
	if req.Lock != nil {
		updates = append(updates, "is_locked = $"+strconv.Itoa(argIdx))
		args = append(args, *req.Lock)
		argIdx++
	}
	// If any grading field is present, record grader metadata
	if req.ManualScore != nil || req.Feedback != nil || req.Status != nil {
		updates = append(updates, "graded_by = $"+strconv.Itoa(argIdx))
		args = append(args, uid)
		argIdx++
		updates = append(updates, "graded_at = NOW()")
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update"})
		return
	}

	query := "UPDATE app.submissions SET "
	for i, u := range updates {
		if i > 0 {
			query += ", "
		}
		query += u
	}
	query += " WHERE id = $" + strconv.Itoa(argIdx)
	args = append(args, submissionID)

	if _, err := h.DB.Exec(ctx, query, args...); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update submission"})
		return
	}

	// If this is a contest submission and a manual score was set, update contest_solves
	if contestID != nil && req.ManualScore != nil {
		score := *req.ManualScore
		if score > 0 {
			_, err = h.DB.Exec(ctx,
				`INSERT INTO app.contest_solves
				     (contest_id, user_id, problem_id, submission_id, points_earned, solved_at)
				 VALUES ($1, $2, $3, $4, $5, $6)
				 ON CONFLICT (contest_id, user_id, problem_id) DO UPDATE
				 SET points_earned = EXCLUDED.points_earned,
				     submission_id = EXCLUDED.submission_id,
				     solved_at     = EXCLUDED.solved_at`,
				*contestID, userOwner, problemID, submissionID, score, submittedAt)
		} else {
			_, _ = h.DB.Exec(ctx,
				`DELETE FROM app.contest_solves
				 WHERE contest_id = $1 AND user_id = $2 AND problem_id = $3`,
				*contestID, userOwner, problemID)
		}

		// Recompute participant aggregate
		var startTime time.Time
		_ = h.DB.QueryRow(ctx,
			`SELECT start_time FROM app.contests WHERE id = $1`, *contestID,
		).Scan(&startTime)
		h.DB.Exec(ctx,
			`UPDATE app.contest_participants
			 SET score = (SELECT COALESCE(SUM(points_earned), 0) FROM app.contest_solves
			              WHERE contest_id = $1 AND user_id = $2),
			     penalty_time = (SELECT COALESCE(SUM(EXTRACT(EPOCH FROM (solved_at - $3::timestamptz)) / 60)::int, 0)
			                     FROM app.contest_solves
			                     WHERE contest_id = $1 AND user_id = $2)
			 WHERE contest_id = $1 AND user_id = $2`,
			*contestID, userOwner, startTime)
	}

	h.logAudit(uid, "submission.grade", "submission", submissionID, map[string]interface{}{
		"manual_score": req.ManualScore, "lock": req.Lock, "status": req.Status,
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"message": "Submission updated"})
}

func isValidSubmissionStatus(s string) bool {
	switch s {
	case "pending", "pending_review", "accepted", "rejected",
		"wrong_answer", "time_limit_exceeded", "memory_limit_exceeded",
		"runtime_error", "compilation_error":
		return true
	}
	return false
}

// AdminRunSubmission runs the stored source code against arbitrary input in the sandbox.
func (h *Handler) AdminRunSubmission(c *gin.Context) {
	submissionID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid submission ID"})
		return
	}

	var req RunSubmissionRequest
	_ = c.ShouldBindJSON(&req)

	var sourceCode, language string
	var timeLimitMs, memoryLimitMb int
	err = h.DB.QueryRow(context.Background(),
		`SELECT s.source_code, s.language, p.time_limit_ms, p.memory_limit_mb
		 FROM app.submissions s
		 JOIN app.problems p ON p.id = s.problem_id
		 WHERE s.id = $1`, submissionID,
	).Scan(&sourceCode, &language, &timeLimitMs, &memoryLimitMb)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Submission not found"})
		return
	}

	cfg := sandbox.DefaultConfig()
	if timeLimitMs > 0 {
		cfg.MaxTimeSec = (timeLimitMs + 999) / 1000
		if cfg.MaxTimeSec < 1 {
			cfg.MaxTimeSec = 1
		}
		if cfg.MaxTimeSec > 10 {
			cfg.MaxTimeSec = 10
		}
	}
	if memoryLimitMb > 0 {
		cfg.MaxMemoryMB = memoryLimitMb
		if cfg.MaxMemoryMB > 256 {
			cfg.MaxMemoryMB = 256
		}
	}

	result := sandbox.RunCpp(sourceCode, req.Input, cfg)
	c.JSON(http.StatusOK, result)
}
