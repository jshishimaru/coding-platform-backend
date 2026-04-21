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
// Generic component types (generators, validators, checkers, interactors, solutions)
// ──────────────────────────────────────────────────────────

type ComponentInfo struct {
	ID          int       `json:"id"`
	ProblemID   int       `json:"problem_id"`
	Name        string    `json:"name"`
	SourceCode  string    `json:"source_code"`
	Description string    `json:"description,omitempty"`
	IsActive    *bool     `json:"is_active,omitempty"`
	CheckerType string    `json:"checker_type,omitempty"`
	ExpVerdict  string    `json:"expected_verdict,omitempty"`
	Tag         string    `json:"tag,omitempty"`
	CreatedBy   int       `json:"created_by"`
	CreatorName string    `json:"creator_name"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateComponentRequest struct {
	Name            string `json:"name" binding:"required"`
	SourceCode      string `json:"source_code" binding:"required"`
	Description     string `json:"description"`
	CheckerType     string `json:"checker_type"`
	ExpectedVerdict string `json:"expected_verdict"`
	Tag             string `json:"tag"`
}

type UpdateComponentRequest struct {
	Name            *string `json:"name"`
	SourceCode      *string `json:"source_code"`
	Description     *string `json:"description"`
	CheckerType     *string `json:"checker_type"`
	ExpectedVerdict *string `json:"expected_verdict"`
	Tag             *string `json:"tag"`
}

type CompileResult struct {
	Success       bool   `json:"success"`
	CompileTimeMs int64  `json:"compile_time_ms"`
	Error         string `json:"error,omitempty"`
	Output        string `json:"output,omitempty"`
}

// componentTable maps component type to database table name.
func componentTable(compType string) string {
	switch compType {
	case "generators":
		return "app.problem_generators"
	case "validators":
		return "app.problem_validators"
	case "checkers":
		return "app.problem_checkers"
	case "interactors":
		return "app.problem_interactors"
	case "solutions":
		return "app.problem_solutions"
	default:
		return ""
	}
}

// ──────────────────────────────────────────────────────────
// GENERATORS
// ──────────────────────────────────────────────────────────

func (h *Handler) AdminListGenerators(c *gin.Context) {
	h.listComponents(c, "generators")
}

func (h *Handler) AdminCreateGenerator(c *gin.Context) {
	h.createComponent(c, "generators")
}

func (h *Handler) AdminUpdateGenerator(c *gin.Context) {
	h.updateComponent(c, "generators")
}

func (h *Handler) AdminDeleteGenerator(c *gin.Context) {
	h.deleteComponent(c, "generators")
}

func (h *Handler) AdminCompileGenerator(c *gin.Context) {
	h.compileComponent(c, "generators")
}

func (h *Handler) AdminActivateGenerator(c *gin.Context) {
	h.activateComponent(c, "generators")
}

// ──────────────────────────────────────────────────────────
// VALIDATORS
// ──────────────────────────────────────────────────────────

func (h *Handler) AdminListValidators(c *gin.Context) {
	h.listComponents(c, "validators")
}

func (h *Handler) AdminCreateValidator(c *gin.Context) {
	h.createComponent(c, "validators")
}

func (h *Handler) AdminUpdateValidator(c *gin.Context) {
	h.updateComponent(c, "validators")
}

func (h *Handler) AdminDeleteValidator(c *gin.Context) {
	h.deleteComponent(c, "validators")
}

func (h *Handler) AdminCompileValidator(c *gin.Context) {
	h.compileComponent(c, "validators")
}

func (h *Handler) AdminActivateValidator(c *gin.Context) {
	h.activateComponent(c, "validators")
}

// ──────────────────────────────────────────────────────────
// CHECKERS
// ──────────────────────────────────────────────────────────

func (h *Handler) AdminListCheckers(c *gin.Context) {
	h.listComponents(c, "checkers")
}

func (h *Handler) AdminCreateChecker(c *gin.Context) {
	h.createComponent(c, "checkers")
}

func (h *Handler) AdminUpdateChecker(c *gin.Context) {
	h.updateComponent(c, "checkers")
}

func (h *Handler) AdminDeleteChecker(c *gin.Context) {
	h.deleteComponent(c, "checkers")
}

func (h *Handler) AdminCompileChecker(c *gin.Context) {
	h.compileComponent(c, "checkers")
}

func (h *Handler) AdminActivateChecker(c *gin.Context) {
	h.activateComponent(c, "checkers")
}

// ──────────────────────────────────────────────────────────
// INTERACTORS
// ──────────────────────────────────────────────────────────

func (h *Handler) AdminListInteractors(c *gin.Context) {
	h.listComponents(c, "interactors")
}

func (h *Handler) AdminCreateInteractor(c *gin.Context) {
	h.createComponent(c, "interactors")
}

func (h *Handler) AdminUpdateInteractor(c *gin.Context) {
	h.updateComponent(c, "interactors")
}

func (h *Handler) AdminDeleteInteractor(c *gin.Context) {
	h.deleteComponent(c, "interactors")
}

func (h *Handler) AdminCompileInteractor(c *gin.Context) {
	h.compileComponent(c, "interactors")
}

func (h *Handler) AdminActivateInteractor(c *gin.Context) {
	h.activateComponent(c, "interactors")
}

// ──────────────────────────────────────────────────────────
// SOLUTIONS
// ──────────────────────────────────────────────────────────

func (h *Handler) AdminListSolutions(c *gin.Context) {
	h.listComponents(c, "solutions")
}

func (h *Handler) AdminCreateSolution(c *gin.Context) {
	h.createComponent(c, "solutions")
}

func (h *Handler) AdminUpdateSolution(c *gin.Context) {
	h.updateComponent(c, "solutions")
}

func (h *Handler) AdminDeleteSolution(c *gin.Context) {
	h.deleteComponent(c, "solutions")
}

func (h *Handler) AdminCompileSolution(c *gin.Context) {
	h.compileComponent(c, "solutions")
}

// ──────────────────────────────────────────────────────────
// Generic CRUD implementations
// ──────────────────────────────────────────────────────────

func (h *Handler) listComponents(c *gin.Context, compType string) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	table := componentTable(compType)
	if table == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid component type"})
		return
	}

	ctx := context.Background()
	var query string

	switch compType {
	case "generators":
		query = `SELECT g.id, g.problem_id, g.name, g.source_code, g.description,
		                g.is_active, g.created_by, u.username, g.created_at, g.updated_at
		         FROM ` + table + ` g
		         JOIN app.users u ON u.id = g.created_by
		         WHERE g.problem_id = $1 ORDER BY g.id`
	case "validators", "interactors":
		query = `SELECT v.id, v.problem_id, v.name, v.source_code, '',
		                v.is_active, v.created_by, u.username, v.created_at, v.updated_at
		         FROM ` + table + ` v
		         JOIN app.users u ON u.id = v.created_by
		         WHERE v.problem_id = $1 ORDER BY v.id`
	case "checkers":
		query = `SELECT ch.id, ch.problem_id, ch.name, ch.source_code, ch.checker_type,
		                ch.is_active, ch.created_by, u.username, ch.created_at, ch.updated_at
		         FROM ` + table + ` ch
		         JOIN app.users u ON u.id = ch.created_by
		         WHERE ch.problem_id = $1 ORDER BY ch.id`
	case "solutions":
		query = `SELECT s.id, s.problem_id, s.name, s.source_code, s.expected_verdict,
		                s.tag, s.created_by, u.username, s.created_at, s.updated_at
		         FROM ` + table + ` s
		         JOIN app.users u ON u.id = s.created_by
		         WHERE s.problem_id = $1 ORDER BY s.id`
	}

	rows, err := h.DB.Query(ctx, query, problemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	items := make([]ComponentInfo, 0)
	for rows.Next() {
		var item ComponentInfo
		item.ProblemID = problemID

		switch compType {
		case "generators":
			var isActive bool
			if err := rows.Scan(&item.ID, &item.ProblemID, &item.Name, &item.SourceCode,
				&item.Description, &isActive, &item.CreatedBy, &item.CreatorName,
				&item.CreatedAt, &item.UpdatedAt); err != nil {
				continue
			}
			item.IsActive = &isActive
		case "validators", "interactors":
			var isActive bool
			var desc string
			if err := rows.Scan(&item.ID, &item.ProblemID, &item.Name, &item.SourceCode,
				&desc, &isActive, &item.CreatedBy, &item.CreatorName,
				&item.CreatedAt, &item.UpdatedAt); err != nil {
				continue
			}
			item.IsActive = &isActive
		case "checkers":
			var isActive bool
			if err := rows.Scan(&item.ID, &item.ProblemID, &item.Name, &item.SourceCode,
				&item.CheckerType, &isActive, &item.CreatedBy, &item.CreatorName,
				&item.CreatedAt, &item.UpdatedAt); err != nil {
				continue
			}
			item.IsActive = &isActive
		case "solutions":
			if err := rows.Scan(&item.ID, &item.ProblemID, &item.Name, &item.SourceCode,
				&item.ExpVerdict, &item.Tag, &item.CreatedBy, &item.CreatorName,
				&item.CreatedAt, &item.UpdatedAt); err != nil {
				continue
			}
		}

		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{compType: items})
}

func (h *Handler) createComponent(c *gin.Context, compType string) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}

	var req CreateComponentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	ctx := context.Background()

	var id int
	var insertErr error

	switch compType {
	case "generators":
		// Auto-activate if this is the first generator for the problem so
		// "Generate tests" works immediately without an extra click.
		var existing int
		_ = h.DB.QueryRow(ctx,
			`SELECT COUNT(*) FROM app.problem_generators WHERE problem_id = $1`, problemID,
		).Scan(&existing)
		active := existing == 0
		insertErr = h.DB.QueryRow(ctx,
			`INSERT INTO app.problem_generators (problem_id, name, source_code, description, is_active, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			problemID, req.Name, req.SourceCode, req.Description, active, uid,
		).Scan(&id)
	case "validators":
		insertErr = h.DB.QueryRow(ctx,
			`INSERT INTO app.problem_validators (problem_id, name, source_code, created_by)
			 VALUES ($1, $2, $3, $4) RETURNING id`,
			problemID, req.Name, req.SourceCode, uid,
		).Scan(&id)
	case "checkers":
		checkerType := req.CheckerType
		if checkerType == "" {
			checkerType = "standard"
		}
		insertErr = h.DB.QueryRow(ctx,
			`INSERT INTO app.problem_checkers (problem_id, name, source_code, checker_type, created_by)
			 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			problemID, req.Name, req.SourceCode, checkerType, uid,
		).Scan(&id)
	case "interactors":
		insertErr = h.DB.QueryRow(ctx,
			`INSERT INTO app.problem_interactors (problem_id, name, source_code, created_by)
			 VALUES ($1, $2, $3, $4) RETURNING id`,
			problemID, req.Name, req.SourceCode, uid,
		).Scan(&id)
	case "solutions":
		expectedVerdict := req.ExpectedVerdict
		if expectedVerdict == "" {
			expectedVerdict = "AC"
		}
		tag := req.Tag
		if tag == "" {
			tag = "main"
		}
		insertErr = h.DB.QueryRow(ctx,
			`INSERT INTO app.problem_solutions (problem_id, name, source_code, expected_verdict, tag, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			problemID, req.Name, req.SourceCode, expectedVerdict, tag, uid,
		).Scan(&id)
	}

	if insertErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create " + compType[:len(compType)-1]})
		return
	}

	h.logAudit(uid, compType[:len(compType)-1]+".create", compType[:len(compType)-1], id, map[string]interface{}{
		"problem_id": problemID, "name": req.Name,
	}, c.ClientIP())

	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func (h *Handler) updateComponent(c *gin.Context, compType string) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}
	compID, err := strconv.Atoi(c.Param("compId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid component ID"})
		return
	}

	var req UpdateComponentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	table := componentTable(compType)
	ctx := context.Background()

	// Build dynamic update
	updates := []string{}
	args := []interface{}{}
	argIdx := 1

	if req.Name != nil {
		updates = append(updates, "name = $"+strconv.Itoa(argIdx))
		args = append(args, *req.Name)
		argIdx++
	}
	if req.SourceCode != nil {
		updates = append(updates, "source_code = $"+strconv.Itoa(argIdx))
		args = append(args, *req.SourceCode)
		argIdx++
	}
	if req.Description != nil && (compType == "generators") {
		updates = append(updates, "description = $"+strconv.Itoa(argIdx))
		args = append(args, *req.Description)
		argIdx++
	}
	if req.CheckerType != nil && compType == "checkers" {
		updates = append(updates, "checker_type = $"+strconv.Itoa(argIdx))
		args = append(args, *req.CheckerType)
		argIdx++
	}
	if req.ExpectedVerdict != nil && compType == "solutions" {
		updates = append(updates, "expected_verdict = $"+strconv.Itoa(argIdx))
		args = append(args, *req.ExpectedVerdict)
		argIdx++
	}
	if req.Tag != nil && compType == "solutions" {
		updates = append(updates, "tag = $"+strconv.Itoa(argIdx))
		args = append(args, *req.Tag)
		argIdx++
	}

	// Always update updated_at
	updates = append(updates, "updated_at = NOW()")

	if len(updates) <= 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update"})
		return
	}

	query := "UPDATE " + table + " SET "
	for i, u := range updates {
		if i > 0 {
			query += ", "
		}
		query += u
	}
	query += " WHERE id = $" + strconv.Itoa(argIdx) + " AND problem_id = $" + strconv.Itoa(argIdx+1)
	args = append(args, compID, problemID)

	_, err = h.DB.Exec(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Updated"})
}

func (h *Handler) deleteComponent(c *gin.Context, compType string) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}
	compID, err := strconv.Atoi(c.Param("compId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid component ID"})
		return
	}

	table := componentTable(compType)
	_, err = h.DB.Exec(context.Background(),
		"DELETE FROM "+table+" WHERE id = $1 AND problem_id = $2", compID, problemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	h.logAudit(uid, compType[:len(compType)-1]+".delete", compType[:len(compType)-1], compID, map[string]interface{}{
		"problem_id": problemID,
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"message": "Deleted"})
}

func (h *Handler) compileComponent(c *gin.Context, compType string) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}
	compID, err := strconv.Atoi(c.Param("compId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid component ID"})
		return
	}

	table := componentTable(compType)
	ctx := context.Background()

	var sourceCode string
	err = h.DB.QueryRow(ctx,
		"SELECT source_code FROM "+table+" WHERE id = $1 AND problem_id = $2",
		compID, problemID,
	).Scan(&sourceCode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Component not found"})
		return
	}

	// Use sandbox to compile and test-run
	cfg := &sandbox.Config{
		MaxTimeSec:     10,
		MaxMemoryMB:    256,
		MaxOutputBytes: 1 * 1024 * 1024,
	}
	result := sandbox.RunCpp(sourceCode, "", cfg)

	compResult := CompileResult{
		CompileTimeMs: result.CompileTimeMs,
	}

	if result.Status == "compilation_error" {
		compResult.Success = false
		compResult.Error = result.Stderr
	} else {
		compResult.Success = true
		compResult.Output = result.Stdout
		if result.Stderr != "" {
			compResult.Error = result.Stderr
		}
	}

	c.JSON(http.StatusOK, compResult)
}

func (h *Handler) activateComponent(c *gin.Context, compType string) {
	problemID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return
	}
	compID, err := strconv.Atoi(c.Param("compId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid component ID"})
		return
	}

	table := componentTable(compType)
	ctx := context.Background()

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer tx.Rollback(ctx)

	// Deactivate all of this type for this problem
	_, _ = tx.Exec(ctx, "UPDATE "+table+" SET is_active = FALSE WHERE problem_id = $1", problemID)
	// Activate this one
	_, err = tx.Exec(ctx, "UPDATE "+table+" SET is_active = TRUE WHERE id = $1 AND problem_id = $2", compID, problemID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to activate"})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Activated"})
}
