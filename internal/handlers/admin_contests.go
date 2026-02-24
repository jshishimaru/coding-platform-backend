package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// ──────────────────────────────────────────────────────────
// Request / Response Types
// ──────────────────────────────────────────────────────────

type AdminCreateContestRequest struct {
	Title              string    `json:"title" binding:"required"`
	Description        string    `json:"description"`
	StartTime          time.Time `json:"start_time" binding:"required"`
	EndTime            time.Time `json:"end_time" binding:"required"`
	IsRated            bool      `json:"is_rated"`
	ScoringType        string    `json:"scoring_type"`
	PenaltyTimeSeconds int       `json:"penalty_time_seconds"`
	FreezeTimeMinutes  *int      `json:"freeze_time_minutes"`
	AllowVirtual       bool      `json:"allow_virtual"`
}

type AdminUpdateContestRequest struct {
	Title              *string    `json:"title"`
	Description        *string    `json:"description"`
	StartTime          *time.Time `json:"start_time"`
	EndTime            *time.Time `json:"end_time"`
	IsRated            *bool      `json:"is_rated"`
	ScoringType        *string    `json:"scoring_type"`
	PenaltyTimeSeconds *int       `json:"penalty_time_seconds"`
	FreezeTimeMinutes  *int       `json:"freeze_time_minutes"`
	AllowVirtual       *bool      `json:"allow_virtual"`
}

type AdminContestSummary struct {
	ID               int       `json:"id"`
	Title            string    `json:"title"`
	StartTime        time.Time `json:"start_time"`
	EndTime          time.Time `json:"end_time"`
	IsRated          bool      `json:"is_rated"`
	ScoringType      string    `json:"scoring_type"`
	Status           string    `json:"status"`
	ProblemCount     int       `json:"problem_count"`
	ParticipantCount int       `json:"participant_count"`
	CreatedBy        int       `json:"created_by"`
	CreatorName      string    `json:"creator_name"`
	CreatedAt        time.Time `json:"created_at"`
}

type AdminContestDetail struct {
	ID                 int                   `json:"id"`
	Title              string                `json:"title"`
	Description        string                `json:"description"`
	StartTime          time.Time             `json:"start_time"`
	EndTime            time.Time             `json:"end_time"`
	IsRated            bool                  `json:"is_rated"`
	ScoringType        string                `json:"scoring_type"`
	Status             string                `json:"status"`
	PenaltyTimeSeconds int                   `json:"penalty_time_seconds"`
	FreezeTimeMinutes  *int                  `json:"freeze_time_minutes"`
	AllowVirtual       bool                  `json:"allow_virtual"`
	CreatedBy          int                   `json:"created_by"`
	CreatorName        string                `json:"creator_name"`
	CreatedAt          time.Time             `json:"created_at"`
	Problems           []AdminContestProblem `json:"problems"`
}

type AdminContestProblem struct {
	ID            int    `json:"id"`
	ProblemID     int    `json:"problem_id"`
	Title         string `json:"title"`
	Slug          string `json:"slug"`
	MaxPoints     int    `json:"max_points"`
	ProblemOrder  int    `json:"problem_order"`
	ScoringConfig string `json:"scoring_config"`
}

type AddContestProblemRequest struct {
	ProblemID     int    `json:"problem_id" binding:"required"`
	MaxPoints     int    `json:"max_points"`
	ProblemOrder  int    `json:"problem_order"`
	ScoringConfig string `json:"scoring_config"`
}

type UpdateContestProblemRequest struct {
	MaxPoints     *int    `json:"max_points"`
	ProblemOrder  *int    `json:"problem_order"`
	ScoringConfig *string `json:"scoring_config"`
}

// ──────────────────────────────────────────────────────────
// Handlers
// ──────────────────────────────────────────────────────────

// AdminListContests lists all contests with admin details.
func (h *Handler) AdminListContests(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	limit := 20
	offset := (page - 1) * limit

	statusFilter := c.Query("status")
	ctx := context.Background()

	query := `SELECT c.id, c.title, c.start_time, c.end_time, c.is_rated, c.scoring_type,
	                  c.status, c.created_by, u.username, c.created_at,
	                  (SELECT COUNT(*) FROM app.contest_problems WHERE contest_id = c.id),
	                  (SELECT COUNT(*) FROM app.contest_participants WHERE contest_id = c.id)
	           FROM app.contests c
	           JOIN app.users u ON u.id = c.created_by
	           WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM app.contests c WHERE 1=1`
	args := []interface{}{}
	countArgs := []interface{}{}
	argIdx := 1

	if statusFilter != "" {
		clause := ` AND c.status = $` + strconv.Itoa(argIdx)
		query += clause
		countQuery += clause
		args = append(args, statusFilter)
		countArgs = append(countArgs, statusFilter)
		argIdx++
	}

	var total int
	_ = h.DB.QueryRow(ctx, countQuery, countArgs...).Scan(&total)

	query += ` ORDER BY c.start_time DESC LIMIT $` + strconv.Itoa(argIdx) + ` OFFSET $` + strconv.Itoa(argIdx+1)
	args = append(args, limit, offset)

	rows, err := h.DB.Query(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	contests := make([]AdminContestSummary, 0)
	for rows.Next() {
		var cs AdminContestSummary
		if err := rows.Scan(&cs.ID, &cs.Title, &cs.StartTime, &cs.EndTime,
			&cs.IsRated, &cs.ScoringType, &cs.Status, &cs.CreatedBy,
			&cs.CreatorName, &cs.CreatedAt, &cs.ProblemCount, &cs.ParticipantCount); err != nil {
			continue
		}
		contests = append(contests, cs)
	}

	c.JSON(http.StatusOK, gin.H{
		"contests": contests,
		"total":    total,
		"page":     page,
		"pages":    (total + limit - 1) / limit,
	})
}

// AdminCreateContest creates a new contest.
func (h *Handler) AdminCreateContest(c *gin.Context) {
	var req AdminCreateContestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.ScoringType == "" {
		req.ScoringType = "icpc"
	}
	if req.ScoringType != "icpc" && req.ScoringType != "ioi" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scoring_type must be 'icpc' or 'ioi'"})
		return
	}
	if req.PenaltyTimeSeconds <= 0 {
		req.PenaltyTimeSeconds = 1200
	}
	if req.EndTime.Before(req.StartTime) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "end_time must be after start_time"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	ctx := context.Background()

	var contestID int
	err := h.DB.QueryRow(ctx,
		`INSERT INTO app.contests (title, description, start_time, end_time, is_rated, scoring_type, status, penalty_time_seconds, freeze_time_minutes, allow_virtual, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, 'draft', $7, $8, $9, $10) RETURNING id`,
		req.Title, req.Description, req.StartTime, req.EndTime,
		req.IsRated, req.ScoringType, req.PenaltyTimeSeconds,
		req.FreezeTimeMinutes, req.AllowVirtual, uid,
	).Scan(&contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create contest"})
		return
	}

	h.logAudit(uid, "contest.create", "contest", contestID, map[string]interface{}{
		"title": req.Title, "scoring_type": req.ScoringType,
	}, c.ClientIP())

	c.JSON(http.StatusCreated, gin.H{"id": contestID})
}

// AdminGetContest returns full contest detail.
func (h *Handler) AdminGetContest(c *gin.Context) {
	contestID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	ctx := context.Background()
	var cd AdminContestDetail
	err = h.DB.QueryRow(ctx,
		`SELECT c.id, c.title, c.description, c.start_time, c.end_time,
		        c.is_rated, c.scoring_type, c.status, c.penalty_time_seconds,
		        c.freeze_time_minutes, c.allow_virtual, c.created_by, u.username, c.created_at
		 FROM app.contests c
		 JOIN app.users u ON u.id = c.created_by
		 WHERE c.id = $1`, contestID,
	).Scan(&cd.ID, &cd.Title, &cd.Description, &cd.StartTime, &cd.EndTime,
		&cd.IsRated, &cd.ScoringType, &cd.Status, &cd.PenaltyTimeSeconds,
		&cd.FreezeTimeMinutes, &cd.AllowVirtual, &cd.CreatedBy, &cd.CreatorName, &cd.CreatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contest not found"})
		return
	}

	// Fetch contest problems
	cd.Problems = make([]AdminContestProblem, 0)
	rows, err := h.DB.Query(ctx,
		`SELECT cp.id, cp.problem_id, p.title, p.slug, cp.max_points, cp.problem_order, cp.scoring_config::text
		 FROM app.contest_problems cp
		 JOIN app.problems p ON p.id = cp.problem_id
		 WHERE cp.contest_id = $1
		 ORDER BY cp.problem_order`, contestID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p AdminContestProblem
			if err := rows.Scan(&p.ID, &p.ProblemID, &p.Title, &p.Slug,
				&p.MaxPoints, &p.ProblemOrder, &p.ScoringConfig); err == nil {
				cd.Problems = append(cd.Problems, p)
			}
		}
	}

	c.JSON(http.StatusOK, cd)
}

// AdminUpdateContest updates contest fields.
func (h *Handler) AdminUpdateContest(c *gin.Context) {
	contestID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	var req AdminUpdateContestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()

	// Check creator or admin
	userID, _ := c.Get("userID")
	uid := userID.(int)
	role, _ := c.Get("role")

	var createdBy int
	_ = h.DB.QueryRow(ctx, `SELECT created_by FROM app.contests WHERE id = $1`, contestID).Scan(&createdBy)
	if role != "admin" && createdBy != uid {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only the contest creator or admin can edit"})
		return
	}

	// Build dynamic update
	updates := []string{}
	args := []interface{}{}
	argIdx := 1

	if req.Title != nil {
		updates = append(updates, "title = $"+strconv.Itoa(argIdx))
		args = append(args, *req.Title)
		argIdx++
	}
	if req.Description != nil {
		updates = append(updates, "description = $"+strconv.Itoa(argIdx))
		args = append(args, *req.Description)
		argIdx++
	}
	if req.StartTime != nil {
		updates = append(updates, "start_time = $"+strconv.Itoa(argIdx))
		args = append(args, *req.StartTime)
		argIdx++
	}
	if req.EndTime != nil {
		updates = append(updates, "end_time = $"+strconv.Itoa(argIdx))
		args = append(args, *req.EndTime)
		argIdx++
	}
	if req.IsRated != nil {
		updates = append(updates, "is_rated = $"+strconv.Itoa(argIdx))
		args = append(args, *req.IsRated)
		argIdx++
	}
	if req.ScoringType != nil {
		updates = append(updates, "scoring_type = $"+strconv.Itoa(argIdx))
		args = append(args, *req.ScoringType)
		argIdx++
	}
	if req.PenaltyTimeSeconds != nil {
		updates = append(updates, "penalty_time_seconds = $"+strconv.Itoa(argIdx))
		args = append(args, *req.PenaltyTimeSeconds)
		argIdx++
	}
	if req.FreezeTimeMinutes != nil {
		updates = append(updates, "freeze_time_minutes = $"+strconv.Itoa(argIdx))
		args = append(args, *req.FreezeTimeMinutes)
		argIdx++
	}
	if req.AllowVirtual != nil {
		updates = append(updates, "allow_virtual = $"+strconv.Itoa(argIdx))
		args = append(args, *req.AllowVirtual)
		argIdx++
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update"})
		return
	}

	query := "UPDATE app.contests SET "
	for i, u := range updates {
		if i > 0 {
			query += ", "
		}
		query += u
	}
	query += " WHERE id = $" + strconv.Itoa(argIdx)
	args = append(args, contestID)

	_, err = h.DB.Exec(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contest"})
		return
	}

	h.logAudit(uid, "contest.update", "contest", contestID, nil, c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "Contest updated"})
}

// AdminDeleteContest deletes a draft contest.
func (h *Handler) AdminDeleteContest(c *gin.Context) {
	contestID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	ctx := context.Background()

	// Only allow deletion of draft contests
	var status string
	err = h.DB.QueryRow(ctx, `SELECT status FROM app.contests WHERE id = $1`, contestID).Scan(&status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contest not found"})
		return
	}
	if status != "draft" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only draft contests can be deleted"})
		return
	}

	_, err = h.DB.Exec(ctx, `DELETE FROM app.contests WHERE id = $1`, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete contest"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	h.logAudit(uid, "contest.delete", "contest", contestID, nil, c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "Contest deleted"})
}

// AdminAddContestProblem adds a problem to a contest.
func (h *Handler) AdminAddContestProblem(c *gin.Context) {
	contestID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	var req AddContestProblemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.MaxPoints <= 0 {
		req.MaxPoints = 100
	}

	ctx := context.Background()

	// Auto-assign order
	if req.ProblemOrder <= 0 {
		var maxOrder int
		_ = h.DB.QueryRow(ctx,
			`SELECT COALESCE(MAX(problem_order), 0) FROM app.contest_problems WHERE contest_id = $1`, contestID,
		).Scan(&maxOrder)
		req.ProblemOrder = maxOrder + 1
	}

	scoringConfig := req.ScoringConfig
	if scoringConfig == "" {
		scoringConfig = "{}"
	}

	var cpID int
	err = h.DB.QueryRow(ctx,
		`INSERT INTO app.contest_problems (contest_id, problem_id, points, problem_order, max_points, scoring_config)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb) RETURNING id`,
		contestID, req.ProblemID, req.MaxPoints, req.ProblemOrder, req.MaxPoints, scoringConfig,
	).Scan(&cpID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add problem to contest"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	h.logAudit(uid, "contest.add_problem", "contest", contestID, map[string]interface{}{
		"problem_id": req.ProblemID, "max_points": req.MaxPoints,
	}, c.ClientIP())

	c.JSON(http.StatusCreated, gin.H{"id": cpID})
}

// AdminUpdateContestProblem updates a problem's settings within a contest.
func (h *Handler) AdminUpdateContestProblem(c *gin.Context) {
	contestID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}
	cpID, err := strconv.Atoi(c.Param("cpId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest problem ID"})
		return
	}

	var req UpdateContestProblemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()
	updates := []string{}
	args := []interface{}{}
	argIdx := 1

	if req.MaxPoints != nil {
		updates = append(updates, "max_points = $"+strconv.Itoa(argIdx))
		updates = append(updates, "points = $"+strconv.Itoa(argIdx))
		args = append(args, *req.MaxPoints)
		argIdx++
	}
	if req.ProblemOrder != nil {
		updates = append(updates, "problem_order = $"+strconv.Itoa(argIdx))
		args = append(args, *req.ProblemOrder)
		argIdx++
	}
	if req.ScoringConfig != nil {
		updates = append(updates, "scoring_config = $"+strconv.Itoa(argIdx)+"::jsonb")
		args = append(args, *req.ScoringConfig)
		argIdx++
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update"})
		return
	}

	query := "UPDATE app.contest_problems SET "
	for i, u := range updates {
		if i > 0 {
			query += ", "
		}
		query += u
	}
	query += " WHERE id = $" + strconv.Itoa(argIdx) + " AND contest_id = $" + strconv.Itoa(argIdx+1)
	args = append(args, cpID, contestID)

	_, err = h.DB.Exec(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Updated"})
}

// AdminRemoveContestProblem removes a problem from a contest.
func (h *Handler) AdminRemoveContestProblem(c *gin.Context) {
	contestID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}
	cpID, err := strconv.Atoi(c.Param("cpId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest problem ID"})
		return
	}

	_, err = h.DB.Exec(context.Background(),
		`DELETE FROM app.contest_problems WHERE id = $1 AND contest_id = $2`, cpID, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove problem"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Problem removed from contest"})
}

// AdminPublishContest moves a contest from draft to upcoming.
func (h *Handler) AdminPublishContest(c *gin.Context) {
	contestID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	ctx := context.Background()
	var status string
	err = h.DB.QueryRow(ctx, `SELECT status FROM app.contests WHERE id = $1`, contestID).Scan(&status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contest not found"})
		return
	}
	if status != "draft" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only draft contests can be published"})
		return
	}

	// Verify at least one problem is assigned
	var problemCount int
	_ = h.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM app.contest_problems WHERE contest_id = $1`, contestID,
	).Scan(&problemCount)
	if problemCount == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Contest must have at least one problem"})
		return
	}

	_, err = h.DB.Exec(ctx,
		`UPDATE app.contests SET status = 'upcoming' WHERE id = $1`, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to publish contest"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	h.logAudit(uid, "contest.publish", "contest", contestID, nil, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"message": "Contest published", "status": "upcoming"})
}

// AdminFinalizeContestAdmin finalizes a contest and applies ratings.
func (h *Handler) AdminFinalizeContestAdmin(c *gin.Context) {
	contestID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	ctx := context.Background()
	var status string
	err = h.DB.QueryRow(ctx, `SELECT status FROM app.contests WHERE id = $1`, contestID).Scan(&status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contest not found"})
		return
	}
	if status != "ended" && status != "running" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only ended or running contests can be finalized"})
		return
	}

	_, err = h.DB.Exec(ctx,
		`UPDATE app.contests SET status = 'finalized' WHERE id = $1`, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to finalize contest"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	h.logAudit(uid, "contest.finalize", "contest", contestID, nil, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"message": "Contest finalized", "status": "finalized"})
}
