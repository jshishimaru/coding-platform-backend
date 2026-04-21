package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
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
	GroupID            *int      `json:"group_id"`
	Proctored          bool      `json:"proctored"`
	GradeVisibility    string    `json:"grade_visibility"`
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
	GroupID            *int       `json:"group_id"`
	ClearGroup         *bool      `json:"clear_group"`
	Proctored          *bool      `json:"proctored"`
	GradeVisibility    *string    `json:"grade_visibility"`
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
	GroupID          *int      `json:"group_id"`
	GroupName        string    `json:"group_name"`
	Proctored        bool      `json:"proctored"`
	GradeVisibility  string    `json:"grade_visibility"`
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
	GroupID            *int                  `json:"group_id"`
	GroupName          string                `json:"group_name"`
	Proctored          bool                  `json:"proctored"`
	GradeVisibility    string                `json:"grade_visibility"`
	Problems           []AdminContestProblem `json:"problems"`
}

type AdminContestProblem struct {
	ID            int    `json:"id"`
	ProblemID     int    `json:"problem_id"`
	Title         string `json:"title"`
	Slug          string `json:"slug"`
	ProblemType   string `json:"problem_type"`
	MaxPoints     int    `json:"max_points"`
	ProblemOrder  int    `json:"problem_order"`
	ScoringConfig string `json:"scoring_config"`
	ScoringMode   string `json:"scoring_mode"`
}

type AddContestProblemRequest struct {
	ProblemID     int    `json:"problem_id" binding:"required"`
	MaxPoints     int    `json:"max_points"`
	ProblemOrder  int    `json:"problem_order"`
	ScoringConfig string `json:"scoring_config"`
	ScoringMode   string `json:"scoring_mode"`
}

type UpdateContestProblemRequest struct {
	MaxPoints     *int    `json:"max_points"`
	ProblemOrder  *int    `json:"problem_order"`
	ScoringConfig *string `json:"scoring_config"`
	ScoringMode   *string `json:"scoring_mode"`
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
	groupIDFilter := c.Query("group_id")
	ctx := context.Background()

	filters := make([]string, 0, 2)
	filterArgs := make([]interface{}, 0, 2)
	argIdx := 1

	// Map the requested *derived* status back to a DB predicate. The DB only
	// stores lifecycle states (draft/upcoming/finalized); running/ended are
	// derived from timestamps relative to NOW(). Keeping this mapping in SQL
	// ensures pagination counts are consistent with the visible rows.
	switch statusFilter {
	case "":
		// no filter
	case "draft", "finalized":
		filters = append(filters, `c.status = $`+strconv.Itoa(argIdx))
		filterArgs = append(filterArgs, statusFilter)
		argIdx++
	case "upcoming":
		filters = append(filters, `c.status = 'upcoming' AND c.start_time > NOW()`)
	case "running":
		filters = append(filters, `c.status = 'upcoming' AND c.start_time <= NOW() AND c.end_time >= NOW()`)
	case "ended":
		filters = append(filters, `c.status = 'upcoming' AND c.end_time < NOW()`)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status filter"})
		return
	}

	if groupIDFilter != "" {
		groupID, err := strconv.Atoi(groupIDFilter)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group_id"})
			return
		}
		filters = append(filters, `c.group_id = $`+strconv.Itoa(argIdx))
		filterArgs = append(filterArgs, groupID)
		argIdx++
	}

	filterClause := ""
	if len(filters) > 0 {
		filterClause = ` AND ` + strings.Join(filters, ` AND `)
	}

	baseQuery := `SELECT c.id, c.title, c.start_time, c.end_time, c.is_rated, c.scoring_type,
	                      c.status, c.created_by, u.username, c.created_at,
	                      (SELECT COUNT(*) FROM app.contest_problems WHERE contest_id = c.id),
	                      (SELECT COUNT(*) FROM app.contest_participants WHERE contest_id = c.id),
	                      c.group_id, COALESCE(g.name, ''), c.proctored, c.grade_visibility
	               FROM app.contests c
	               JOIN app.users u ON u.id = c.created_by
	               LEFT JOIN app.groups g ON g.id = c.group_id
	               WHERE 1=1` + filterClause
	countQuery := `SELECT COUNT(*) FROM app.contests c WHERE 1=1` + filterClause

	var total int
	_ = h.DB.QueryRow(ctx, countQuery, filterArgs...).Scan(&total)

	listArgs := append([]interface{}{}, filterArgs...)
	nextIdx := len(listArgs) + 1
	listQuery := baseQuery + ` ORDER BY c.start_time DESC LIMIT $` +
		strconv.Itoa(nextIdx) + ` OFFSET $` + strconv.Itoa(nextIdx+1)
	listArgs = append(listArgs, limit, offset)

	rows, err := h.DB.Query(ctx, listQuery, listArgs...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	contests := make([]AdminContestSummary, 0)
	for rows.Next() {
		var cs AdminContestSummary
		var dbStatus string
		if err := rows.Scan(&cs.ID, &cs.Title, &cs.StartTime, &cs.EndTime,
			&cs.IsRated, &cs.ScoringType, &dbStatus, &cs.CreatedBy,
			&cs.CreatorName, &cs.CreatedAt, &cs.ProblemCount, &cs.ParticipantCount,
			&cs.GroupID, &cs.GroupName, &cs.Proctored, &cs.GradeVisibility); err != nil {
			continue
		}
		cs.Status = AdminContestStatus(dbStatus, cs.StartTime, cs.EndTime)
		contests = append(contests, cs)
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  contests,
		"total": total,
		"page":  page,
		"pages": (total + limit - 1) / limit,
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

	// Group contests are always unrated
	if req.GroupID != nil {
		req.IsRated = false
	}
	if req.GradeVisibility == "" {
		req.GradeVisibility = "private"
	}
	if req.GradeVisibility != "private" && req.GradeVisibility != "group" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "grade_visibility must be 'private' or 'group'"})
		return
	}

	var contestID int
	err := h.DB.QueryRow(ctx,
		`INSERT INTO app.contests (title, description, start_time, end_time, is_rated, scoring_type,
		                           status, penalty_time_seconds, freeze_time_minutes, allow_virtual,
		                           created_by, group_id, proctored, grade_visibility)
		 VALUES ($1, $2, $3, $4, $5, $6, 'draft', $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		req.Title, req.Description, req.StartTime, req.EndTime,
		req.IsRated, req.ScoringType, req.PenaltyTimeSeconds,
		req.FreezeTimeMinutes, req.AllowVirtual, uid,
		req.GroupID, req.Proctored, req.GradeVisibility,
	).Scan(&contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create contest"})
		return
	}

	h.logAudit(uid, "contest.create", "contest", contestID, map[string]interface{}{
		"title": req.Title, "scoring_type": req.ScoringType,
		"group_id": req.GroupID, "proctored": req.Proctored,
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
	var dbStatus string
	err = h.DB.QueryRow(ctx,
		`SELECT c.id, c.title, c.description, c.start_time, c.end_time,
		        c.is_rated, c.scoring_type, c.status, c.penalty_time_seconds,
		        c.freeze_time_minutes, c.allow_virtual, c.created_by, u.username, c.created_at,
		        c.group_id, COALESCE(g.name, ''), c.proctored, c.grade_visibility
		 FROM app.contests c
		 JOIN app.users u ON u.id = c.created_by
		 LEFT JOIN app.groups g ON g.id = c.group_id
		 WHERE c.id = $1`, contestID,
	).Scan(&cd.ID, &cd.Title, &cd.Description, &cd.StartTime, &cd.EndTime,
		&cd.IsRated, &cd.ScoringType, &dbStatus, &cd.PenaltyTimeSeconds,
		&cd.FreezeTimeMinutes, &cd.AllowVirtual, &cd.CreatedBy, &cd.CreatorName, &cd.CreatedAt,
		&cd.GroupID, &cd.GroupName, &cd.Proctored, &cd.GradeVisibility)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contest not found"})
		return
	}
	cd.Status = AdminContestStatus(dbStatus, cd.StartTime, cd.EndTime)

	// Fetch contest problems
	cd.Problems = make([]AdminContestProblem, 0)
	rows, err := h.DB.Query(ctx,
		`SELECT cp.id, cp.problem_id, p.title, p.slug, p.problem_type,
		        cp.max_points, cp.problem_order, cp.scoring_config::text, cp.scoring_mode
		 FROM app.contest_problems cp
		 JOIN app.problems p ON p.id = cp.problem_id
		 WHERE cp.contest_id = $1
		 ORDER BY cp.problem_order`, contestID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p AdminContestProblem
			if err := rows.Scan(&p.ID, &p.ProblemID, &p.Title, &p.Slug, &p.ProblemType,
				&p.MaxPoints, &p.ProblemOrder, &p.ScoringConfig, &p.ScoringMode); err == nil {
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
	if req.Proctored != nil {
		updates = append(updates, "proctored = $"+strconv.Itoa(argIdx))
		args = append(args, *req.Proctored)
		argIdx++
	}
	if req.GradeVisibility != nil {
		if *req.GradeVisibility != "private" && *req.GradeVisibility != "group" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "grade_visibility must be 'private' or 'group'"})
			return
		}
		updates = append(updates, "grade_visibility = $"+strconv.Itoa(argIdx))
		args = append(args, *req.GradeVisibility)
		argIdx++
	}
	if req.ClearGroup != nil && *req.ClearGroup {
		updates = append(updates, "group_id = NULL")
	} else if req.GroupID != nil {
		updates = append(updates, "group_id = $"+strconv.Itoa(argIdx))
		args = append(args, *req.GroupID)
		argIdx++
		// Group contests are always unrated
		updates = append(updates, "is_rated = FALSE")
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

	// Verify the problem exists. Draft (unpublished) problems are allowed
	// inside contests on purpose: contest participants get contest-gated
	// access to the problem through the contest detail endpoint, so adding
	// a not-yet-public problem to a contest is a legitimate workflow.
	var exists bool
	err = h.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM app.problems WHERE id = $1)`, req.ProblemID,
	).Scan(&exists)
	if err != nil || !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Problem not found"})
		return
	}

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
	scoringMode := req.ScoringMode
	if scoringMode == "" {
		scoringMode = "all_or_nothing"
	}
	if scoringMode != "all_or_nothing" && scoringMode != "partial" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scoring_mode must be 'all_or_nothing' or 'partial'"})
		return
	}

	var cpID int
	err = h.DB.QueryRow(ctx,
		`INSERT INTO app.contest_problems (contest_id, problem_id, points, problem_order,
		                                   max_points, scoring_config, scoring_mode)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7) RETURNING id`,
		contestID, req.ProblemID, req.MaxPoints, req.ProblemOrder,
		req.MaxPoints, scoringConfig, scoringMode,
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
	if req.ScoringMode != nil {
		if *req.ScoringMode != "all_or_nothing" && *req.ScoringMode != "partial" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "scoring_mode must be 'all_or_nothing' or 'partial'"})
			return
		}
		updates = append(updates, "scoring_mode = $"+strconv.Itoa(argIdx))
		args = append(args, *req.ScoringMode)
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
	var dbStatus string
	var startTime, endTime time.Time
	err = h.DB.QueryRow(ctx,
		`SELECT status, start_time, end_time FROM app.contests WHERE id = $1`, contestID,
	).Scan(&dbStatus, &startTime, &endTime)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contest not found"})
		return
	}

	// Derived status is the single source of truth for the admin UI. A
	// contest can be finalized when it is currently running or has already
	// ended (but not while still upcoming, draft, or already finalized).
	derived := AdminContestStatus(dbStatus, startTime, endTime)
	if derived != "running" && derived != "ended" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Only running or ended contests can be finalized (current: " + derived + ")",
		})
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
