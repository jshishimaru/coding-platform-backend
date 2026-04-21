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

type AdminCreateProblemRequest struct {
	Title         string `json:"title" binding:"required"`
	Slug          string `json:"slug" binding:"required"`
	Statement     string `json:"statement"`
	Difficulty    string `json:"difficulty"`
	TimeLimitMs   int    `json:"time_limit_ms"`
	MemoryLimitMb int    `json:"memory_limit_mb"`
	CheckerCode   string `json:"checker_code"`
	Points        *int   `json:"points"`
	ProblemType   string `json:"problem_type"`
	Tags          []int  `json:"tags"`
}

type AdminUpdateProblemRequest struct {
	Title         *string `json:"title"`
	Statement     *string `json:"statement"`
	Difficulty    *string `json:"difficulty"`
	TimeLimitMs   *int    `json:"time_limit_ms"`
	MemoryLimitMb *int    `json:"memory_limit_mb"`
	CheckerCode   *string `json:"checker_code"`
	Points        *int    `json:"points"`
	ProblemType   *string `json:"problem_type"`
	Tags          *[]int  `json:"tags"`
}

type AdminProblemSummary struct {
	ID            int        `json:"id"`
	Title         string     `json:"title"`
	Slug          string     `json:"slug"`
	Difficulty    string     `json:"difficulty"`
	TimeLimitMs   int        `json:"time_limit_ms"`
	MemoryLimitMb int        `json:"memory_limit_mb"`
	Points        *int       `json:"points"`
	ProblemType   string     `json:"problem_type"`
	CreatedBy     int        `json:"created_by"`
	CreatorName   string     `json:"creator_name"`
	TestCount     int        `json:"test_count"`
	CreatedAt     time.Time  `json:"created_at"`
	PublishedAt   *time.Time `json:"published_at"`
	Status        string     `json:"status"` // "draft" or "published"
}

type AdminProblemDetail struct {
	ID            int        `json:"id"`
	Title         string     `json:"title"`
	Slug          string     `json:"slug"`
	Statement     string     `json:"statement"`
	Difficulty    string     `json:"difficulty"`
	TimeLimitMs   int        `json:"time_limit_ms"`
	MemoryLimitMb int        `json:"memory_limit_mb"`
	CheckerCode   string     `json:"checker_code"`
	Points        *int       `json:"points"`
	ProblemType   string     `json:"problem_type"`
	CreatedBy     int        `json:"created_by"`
	CreatorName   string     `json:"creator_name"`
	ContestID     *int       `json:"contest_id"`
	CreatedAt     time.Time  `json:"created_at"`
	PublishedAt   *time.Time `json:"published_at"`
	Status        string     `json:"status"` // "draft" or "published"
	Tags          []TagInfo  `json:"tags"`
}

// problemPublishStatus derives the display status from published_at.
func problemPublishStatus(publishedAt *time.Time) string {
	if publishedAt != nil {
		return "published"
	}
	return "draft"
}

type TagInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type ProblemRevisionInfo struct {
	ID            int       `json:"id"`
	ProblemID     int       `json:"problem_id"`
	Revision      int       `json:"revision"`
	Title         string    `json:"title"`
	Difficulty    string    `json:"difficulty"`
	TimeLimitMs   int       `json:"time_limit_ms"`
	MemoryLimitMb int       `json:"memory_limit_mb"`
	Points        *int      `json:"points"`
	IsActive      bool      `json:"is_active"`
	CreatedBy     int       `json:"created_by"`
	CreatorName   string    `json:"creator_name"`
	CreatedAt     time.Time `json:"created_at"`
}

type ProblemAccessEntry struct {
	ID        int       `json:"id"`
	UserID    int       `json:"user_id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	GrantedBy int       `json:"granted_by"`
	GrantedAt time.Time `json:"granted_at"`
}

type GrantAccessRequest struct {
	UserID int    `json:"user_id" binding:"required"`
	Role   string `json:"role" binding:"required"`
}

// ──────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────

func (h *Handler) logAudit(userID int, action, entityType string, entityID int, details map[string]interface{}, ip string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = h.DB.Exec(ctx,
		`INSERT INTO app.admin_audit_log (user_id, action, entity_type, entity_id, details, ip_address)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, action, entityType, entityID, details, ip,
	)
}

// ──────────────────────────────────────────────────────────
// Handlers
// ──────────────────────────────────────────────────────────

// AdminListProblems lists all problems (admin sees all, setter sees own).
func (h *Handler) AdminListProblems(c *gin.Context) {
	role, _ := c.Get("role")
	userID, _ := c.Get("userID")
	uid := userID.(int)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	limit := 20
	offset := (page - 1) * limit

	search := c.Query("search")
	difficulty := c.Query("difficulty")
	statusFilter := c.Query("status") // "" | "draft" | "published"

	// Build query dynamically
	query := `SELECT p.id, p.title, p.slug, p.difficulty, p.time_limit_ms, p.memory_limit_mb,
	                  p.points, p.problem_type, p.created_by, u.username,
	                  (SELECT COUNT(*) FROM app.test_cases WHERE problem_id = p.id),
	                  p.created_at, p.published_at
	           FROM app.problems p
	           JOIN app.users u ON u.id = p.created_by
	           WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM app.problems p WHERE 1=1`

	args := []interface{}{}
	countArgs := []interface{}{}
	argIdx := 1

	// Setter can only see their own problems or ones they have access to
	if role != "admin" {
		clause := ` AND (p.created_by = $` + strconv.Itoa(argIdx) + ` OR p.id IN (SELECT problem_id FROM app.problem_access WHERE user_id = $` + strconv.Itoa(argIdx) + `))`
		query += clause
		countQuery += clause
		args = append(args, uid)
		countArgs = append(countArgs, uid)
		argIdx++
	}

	if search != "" {
		clause := ` AND (LOWER(p.title) LIKE $` + strconv.Itoa(argIdx) + ` OR LOWER(p.slug) LIKE $` + strconv.Itoa(argIdx) + `)`
		query += clause
		countQuery += clause
		searchTerm := "%" + strings.ToLower(search) + "%"
		args = append(args, searchTerm)
		countArgs = append(countArgs, searchTerm)
		argIdx++
	}

	if difficulty != "" {
		clause := ` AND p.difficulty = $` + strconv.Itoa(argIdx)
		query += clause
		countQuery += clause
		args = append(args, difficulty)
		countArgs = append(countArgs, difficulty)
		argIdx++
	}

	switch statusFilter {
	case "":
		// no-op
	case "draft":
		clause := ` AND p.published_at IS NULL`
		query += clause
		countQuery += clause
	case "published":
		clause := ` AND p.published_at IS NOT NULL`
		query += clause
		countQuery += clause
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status filter (expected 'draft' or 'published')"})
		return
	}

	query += ` ORDER BY p.id DESC LIMIT $` + strconv.Itoa(argIdx) + ` OFFSET $` + strconv.Itoa(argIdx+1)
	args = append(args, limit, offset)

	ctx := context.Background()

	// Get total count
	var total int
	err := h.DB.QueryRow(ctx, countQuery, countArgs...).Scan(&total)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	rows, err := h.DB.Query(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	problems := make([]AdminProblemSummary, 0)
	for rows.Next() {
		var p AdminProblemSummary
		if err := rows.Scan(&p.ID, &p.Title, &p.Slug, &p.Difficulty,
			&p.TimeLimitMs, &p.MemoryLimitMb, &p.Points, &p.ProblemType, &p.CreatedBy,
			&p.CreatorName, &p.TestCount, &p.CreatedAt, &p.PublishedAt); err != nil {
			continue
		}
		p.Status = problemPublishStatus(p.PublishedAt)
		problems = append(problems, p)
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  problems,
		"total": total,
		"page":  page,
		"pages": (total + limit - 1) / limit,
	})
}

// AdminCreateProblem creates a new problem with an initial revision.
func (h *Handler) AdminCreateProblem(c *gin.Context) {
	var req AdminCreateProblemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.TimeLimitMs <= 0 {
		req.TimeLimitMs = 2000
	}
	if req.MemoryLimitMb <= 0 {
		req.MemoryLimitMb = 256
	}
	if req.Difficulty == "" {
		req.Difficulty = "medium"
	}
	if req.ProblemType == "" {
		req.ProblemType = "standard"
	}
	if req.ProblemType != "standard" && req.ProblemType != "subjective" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "problem_type must be 'standard' or 'subjective'"})
		return
	}
	req.Slug = strings.ToLower(strings.ReplaceAll(req.Slug, " ", "-"))

	userID, _ := c.Get("userID")
	uid := userID.(int)
	ctx := context.Background()

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer tx.Rollback(ctx)

	// Insert problem
	var problemID int
	err = tx.QueryRow(ctx,
		`INSERT INTO app.problems (title, slug, statement, difficulty, time_limit_ms, memory_limit_mb, checker_code, created_by, points, problem_type)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		req.Title, req.Slug, req.Statement, req.Difficulty,
		req.TimeLimitMs, req.MemoryLimitMb, req.CheckerCode, uid, req.Points, req.ProblemType,
	).Scan(&problemID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			c.JSON(http.StatusConflict, gin.H{"error": "A problem with this slug already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create problem"})
		return
	}

	// Create initial revision (revision 1, active)
	_, err = tx.Exec(ctx,
		`INSERT INTO app.problem_revisions (problem_id, revision, title, statement, difficulty, time_limit_ms, memory_limit_mb, checker_code, points, is_active, created_by)
		 VALUES ($1, 1, $2, $3, $4, $5, $6, $7, $8, TRUE, $9)`,
		problemID, req.Title, req.Statement, req.Difficulty,
		req.TimeLimitMs, req.MemoryLimitMb, req.CheckerCode, req.Points, uid,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create revision"})
		return
	}

	// Assign tags
	for _, tagID := range req.Tags {
		_, _ = tx.Exec(ctx,
			`INSERT INTO app.problem_tags (problem_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			problemID, tagID,
		)
	}

	// Grant owner access
	_, _ = tx.Exec(ctx,
		`INSERT INTO app.problem_access (problem_id, user_id, role, granted_by)
		 VALUES ($1, $2, 'owner', $2) ON CONFLICT DO NOTHING`,
		problemID, uid,
	)

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	h.logAudit(uid, "problem.create", "problem", problemID, map[string]interface{}{
		"title": req.Title, "slug": req.Slug,
	}, c.ClientIP())

	c.JSON(http.StatusCreated, gin.H{"id": problemID, "slug": req.Slug})
}

// AdminGetProblem returns full problem detail for the admin editor.
func (h *Handler) AdminGetProblem(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	ctx := context.Background()
	var p AdminProblemDetail
	err = h.DB.QueryRow(ctx,
		`SELECT p.id, p.title, p.slug, p.statement, p.difficulty, p.time_limit_ms, p.memory_limit_mb,
		        p.checker_code, p.points, p.problem_type, p.created_by, u.username, p.contest_id, p.created_at, p.published_at
		 FROM app.problems p
		 JOIN app.users u ON u.id = p.created_by
		 WHERE p.id = $1`, problemID,
	).Scan(&p.ID, &p.Title, &p.Slug, &p.Statement, &p.Difficulty,
		&p.TimeLimitMs, &p.MemoryLimitMb, &p.CheckerCode, &p.Points, &p.ProblemType,
		&p.CreatedBy, &p.CreatorName, &p.ContestID, &p.CreatedAt, &p.PublishedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Problem not found"})
		return
	}
	p.Status = problemPublishStatus(p.PublishedAt)

	// Fetch tags
	p.Tags = make([]TagInfo, 0)
	rows, err := h.DB.Query(ctx,
		`SELECT t.id, t.name FROM app.tags t
		 JOIN app.problem_tags pt ON pt.tag_id = t.id
		 WHERE pt.problem_id = $1 ORDER BY t.name`, problemID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var t TagInfo
			if err := rows.Scan(&t.ID, &t.Name); err == nil {
				p.Tags = append(p.Tags, t)
			}
		}
	}

	c.JSON(http.StatusOK, p)
}

// AdminUpdateProblem updates problem fields and creates a new revision.
func (h *Handler) AdminUpdateProblem(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	var req AdminUpdateProblemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

	// Fetch current problem state
	var title, statement, difficulty, checkerCode, problemType string
	var timeLimitMs, memoryLimitMb int
	var points *int
	err = tx.QueryRow(ctx,
		`SELECT title, statement, difficulty, time_limit_ms, memory_limit_mb, checker_code, points, problem_type
		 FROM app.problems WHERE id = $1`, problemID,
	).Scan(&title, &statement, &difficulty, &timeLimitMs, &memoryLimitMb, &checkerCode, &points, &problemType)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Problem not found"})
		return
	}

	// Apply updates
	if req.Title != nil {
		title = *req.Title
	}
	if req.Statement != nil {
		statement = *req.Statement
	}
	if req.Difficulty != nil {
		difficulty = *req.Difficulty
	}
	if req.TimeLimitMs != nil {
		timeLimitMs = *req.TimeLimitMs
	}
	if req.MemoryLimitMb != nil {
		memoryLimitMb = *req.MemoryLimitMb
	}
	if req.CheckerCode != nil {
		checkerCode = *req.CheckerCode
	}
	if req.Points != nil {
		points = req.Points
	}
	if req.ProblemType != nil {
		if *req.ProblemType != "standard" && *req.ProblemType != "subjective" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "problem_type must be 'standard' or 'subjective'"})
			return
		}
		problemType = *req.ProblemType
	}

	// Update the problem row
	_, err = tx.Exec(ctx,
		`UPDATE app.problems SET title=$1, statement=$2, difficulty=$3, time_limit_ms=$4,
		        memory_limit_mb=$5, checker_code=$6, points=$7, problem_type=$8
		 WHERE id=$9`,
		title, statement, difficulty, timeLimitMs, memoryLimitMb, checkerCode, points, problemType, problemID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update problem"})
		return
	}

	// Deactivate old revisions and create new one
	_, _ = tx.Exec(ctx, `UPDATE app.problem_revisions SET is_active = FALSE WHERE problem_id = $1`, problemID)

	var nextRev int
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(revision), 0) + 1 FROM app.problem_revisions WHERE problem_id = $1`, problemID,
	).Scan(&nextRev)
	if err != nil {
		nextRev = 1
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO app.problem_revisions (problem_id, revision, title, statement, difficulty, time_limit_ms, memory_limit_mb, checker_code, points, is_active, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, TRUE, $10)`,
		problemID, nextRev, title, statement, difficulty, timeLimitMs, memoryLimitMb, checkerCode, points, uid,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create revision"})
		return
	}

	// Update tags if provided
	if req.Tags != nil {
		_, _ = tx.Exec(ctx, `DELETE FROM app.problem_tags WHERE problem_id = $1`, problemID)
		for _, tagID := range *req.Tags {
			_, _ = tx.Exec(ctx,
				`INSERT INTO app.problem_tags (problem_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				problemID, tagID,
			)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	h.logAudit(uid, "problem.update", "problem", problemID, map[string]interface{}{
		"revision": nextRev,
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"message": "Problem updated", "revision": nextRev})
}

// AdminDeleteProblem soft-deletes a problem (admin only).
func (h *Handler) AdminDeleteProblem(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	ctx := context.Background()

	// For now, do a hard delete since there's no soft-delete column
	// In production, consider adding a 'deleted_at' column
	_, err = h.DB.Exec(ctx, `DELETE FROM app.problems WHERE id = $1`, problemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete problem"})
		return
	}

	h.logAudit(uid, "problem.delete", "problem", problemID, nil, c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "Problem deleted"})
}

// AdminListRevisions lists all revisions for a problem.
func (h *Handler) AdminListRevisions(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	rows, err := h.DB.Query(context.Background(),
		`SELECT r.id, r.problem_id, r.revision, r.title, r.difficulty,
		        r.time_limit_ms, r.memory_limit_mb, r.points, r.is_active,
		        r.created_by, u.username, r.created_at
		 FROM app.problem_revisions r
		 JOIN app.users u ON u.id = r.created_by
		 WHERE r.problem_id = $1
		 ORDER BY r.revision DESC`, problemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	revisions := make([]ProblemRevisionInfo, 0)
	for rows.Next() {
		var r ProblemRevisionInfo
		if err := rows.Scan(&r.ID, &r.ProblemID, &r.Revision, &r.Title, &r.Difficulty,
			&r.TimeLimitMs, &r.MemoryLimitMb, &r.Points, &r.IsActive,
			&r.CreatedBy, &r.CreatorName, &r.CreatedAt); err != nil {
			continue
		}
		revisions = append(revisions, r)
	}

	c.JSON(http.StatusOK, gin.H{"revisions": revisions})
}

// AdminActivateRevision sets a specific revision as active and updates the problem.
func (h *Handler) AdminActivateRevision(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}
	revStr := c.Param("rev")
	revNum, err := strconv.Atoi(revStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid revision number"})
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

	// Get the revision data
	var title, statement, difficulty, checkerCode string
	var timeLimitMs, memoryLimitMb int
	var points *int
	err = tx.QueryRow(ctx,
		`SELECT title, statement, difficulty, time_limit_ms, memory_limit_mb, checker_code, points
		 FROM app.problem_revisions WHERE problem_id = $1 AND revision = $2`,
		problemID, revNum,
	).Scan(&title, &statement, &difficulty, &timeLimitMs, &memoryLimitMb, &checkerCode, &points)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Revision not found"})
		return
	}

	// Deactivate all, activate this one
	_, _ = tx.Exec(ctx, `UPDATE app.problem_revisions SET is_active = FALSE WHERE problem_id = $1`, problemID)
	_, _ = tx.Exec(ctx, `UPDATE app.problem_revisions SET is_active = TRUE WHERE problem_id = $1 AND revision = $2`, problemID, revNum)

	// Update the problem with revision data
	_, err = tx.Exec(ctx,
		`UPDATE app.problems SET title=$1, statement=$2, difficulty=$3, time_limit_ms=$4,
		        memory_limit_mb=$5, checker_code=$6, points=$7
		 WHERE id=$8`,
		title, statement, difficulty, timeLimitMs, memoryLimitMb, checkerCode, points, problemID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update problem"})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	h.logAudit(uid, "problem.activate_revision", "problem", problemID, map[string]interface{}{
		"revision": revNum,
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"message": "Revision activated", "revision": revNum})
}

// AdminPublishProblem publishes the active revision to the live database.
func (h *Handler) AdminPublishProblem(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
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

	// Get the active revision
	var title, statement, difficulty, checkerCode string
	var timeLimitMs, memoryLimitMb int
	var points *int
	err = tx.QueryRow(ctx,
		`SELECT title, statement, difficulty, time_limit_ms, memory_limit_mb, checker_code, points
		 FROM app.problem_revisions WHERE problem_id = $1 AND is_active = TRUE`,
		problemID,
	).Scan(&title, &statement, &difficulty, &timeLimitMs, &memoryLimitMb, &checkerCode, &points)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active revision found. Create or activate a revision first."})
		return
	}

	// Copy active checker to problem's checker_code
	var activeCheckerCode string
	err = tx.QueryRow(ctx,
		`SELECT source_code FROM app.problem_checkers WHERE problem_id = $1 AND is_active = TRUE LIMIT 1`,
		problemID,
	).Scan(&activeCheckerCode)
	if err == nil && activeCheckerCode != "" {
		checkerCode = activeCheckerCode
	}

	// Update the problem with active revision data + checker, and stamp
	// published_at to make it visible to students. Using COALESCE keeps the
	// original publication timestamp on subsequent re-publishes (e.g. after
	// activating a new revision) so you can tell when a problem first went
	// live.
	_, err = tx.Exec(ctx,
		`UPDATE app.problems SET title=$1, statement=$2, difficulty=$3, time_limit_ms=$4,
		        memory_limit_mb=$5, checker_code=$6, points=$7,
		        published_at = COALESCE(published_at, NOW())
		 WHERE id=$8`,
		title, statement, difficulty, timeLimitMs, memoryLimitMb, checkerCode, points, problemID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to publish problem"})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	h.logAudit(uid, "problem.publish", "problem", problemID, nil, c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "Problem published successfully"})
}

// AdminUnpublishProblem takes a published problem back to draft state so it
// disappears from the student-facing listings and detail page. Existing
// submissions are preserved; only new submissions are blocked.
func (h *Handler) AdminUnpublishProblem(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	ctx := context.Background()

	res, err := h.DB.Exec(ctx,
		`UPDATE app.problems SET published_at = NULL WHERE id = $1`, problemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to unpublish problem"})
		return
	}
	if res.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Problem not found"})
		return
	}

	h.logAudit(uid, "problem.unpublish", "problem", problemID, nil, c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "Problem unpublished"})
}

// ──────────────────────────────────────────────────────────
// Tag management (admin-side, uses IDs not names)
// ──────────────────────────────────────────────────────────

// AdminListTags returns every tag with its id and name. The public /api/tags
// endpoint only returns names, which is insufficient for the admin UI where
// problem.tags is persisted by id.
func (h *Handler) AdminListTags(c *gin.Context) {
	rows, err := h.DB.Query(context.Background(),
		`SELECT id, name FROM app.tags ORDER BY name`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	tags := make([]TagInfo, 0)
	for rows.Next() {
		var t TagInfo
		if err := rows.Scan(&t.ID, &t.Name); err == nil {
			tags = append(tags, t)
		}
	}
	c.JSON(http.StatusOK, gin.H{"tags": tags})
}

type createTagRequest struct {
	Name string `json:"name" binding:"required"`
}

// AdminCreateTag creates a new tag and returns its id. Idempotent: if a tag
// with the same (case-sensitive) name already exists, the existing row is
// returned instead of erroring out, so the admin UI can treat this as
// "upsert by name".
func (h *Handler) AdminCreateTag(c *gin.Context) {
	var req createTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Tag name cannot be empty"})
		return
	}

	ctx := context.Background()
	var t TagInfo
	err := h.DB.QueryRow(ctx,
		`INSERT INTO app.tags (name) VALUES ($1)
		 ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		 RETURNING id, name`, req.Name,
	).Scan(&t.ID, &t.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create tag"})
		return
	}
	c.JSON(http.StatusCreated, t)
}

// AdminListProblemAccess lists users with access to a problem.
func (h *Handler) AdminListProblemAccess(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	rows, err := h.DB.Query(context.Background(),
		`SELECT pa.id, pa.user_id, u.username, pa.role, pa.granted_by, pa.granted_at
		 FROM app.problem_access pa
		 JOIN app.users u ON u.id = pa.user_id
		 WHERE pa.problem_id = $1
		 ORDER BY pa.granted_at`, problemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	entries := make([]ProblemAccessEntry, 0)
	for rows.Next() {
		var e ProblemAccessEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.Username, &e.Role, &e.GrantedBy, &e.GrantedAt); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	c.JSON(http.StatusOK, gin.H{"access": entries})
}

// AdminGrantProblemAccess grants a user access to a problem.
func (h *Handler) AdminGrantProblemAccess(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	var req GrantAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	validRoles := map[string]bool{"viewer": true, "tester": true, "editor": true, "owner": true}
	if !validRoles[req.Role] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role. Must be: viewer, tester, editor, or owner"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)

	_, err = h.DB.Exec(context.Background(),
		`INSERT INTO app.problem_access (problem_id, user_id, role, granted_by)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (problem_id, user_id) DO UPDATE SET role = $3, granted_by = $4, granted_at = NOW()`,
		problemID, req.UserID, req.Role, uid,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to grant access"})
		return
	}

	h.logAudit(uid, "problem.grant_access", "problem", problemID, map[string]interface{}{
		"target_user_id": req.UserID, "role": req.Role,
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"message": "Access granted"})
}

// AdminRevokeProblemAccess revokes a user's access to a problem.
func (h *Handler) AdminRevokeProblemAccess(c *gin.Context) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}
	targetUserID, err := strconv.Atoi(c.Param("userId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)

	_, err = h.DB.Exec(context.Background(),
		`DELETE FROM app.problem_access WHERE problem_id = $1 AND user_id = $2`,
		problemID, targetUserID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to revoke access"})
		return
	}

	h.logAudit(uid, "problem.revoke_access", "problem", problemID, map[string]interface{}{
		"target_user_id": targetUserID,
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"message": "Access revoked"})
}
