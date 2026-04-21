package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// AdminDashboard has moved to admin_dashboard.go.

// ──────────────────────────────────────────────────────────
// Request / Response Types
// ──────────────────────────────────────────────────────────

type AdminUserInfo struct {
	ID        int        `json:"id"`
	Username  string     `json:"username"`
	Email     string     `json:"email"`
	Role      string     `json:"role"`
	Rating    int        `json:"rating"`
	IsBanned  bool       `json:"is_banned"`
	BannedAt  *time.Time `json:"banned_at,omitempty"`
	BannedBy  *int       `json:"banned_by,omitempty"`
	BanReason string     `json:"ban_reason"`
	CreatedAt time.Time  `json:"created_at"`
}

type AdminUserDetail struct {
	ID              int        `json:"id"`
	Username        string     `json:"username"`
	Email           string     `json:"email"`
	Role            string     `json:"role"`
	Rating          int        `json:"rating"`
	IsBanned        bool       `json:"is_banned"`
	BannedAt        *time.Time `json:"banned_at,omitempty"`
	BannedBy        *int       `json:"banned_by,omitempty"`
	BanReason       string     `json:"ban_reason"`
	CreatedAt       time.Time  `json:"created_at"`
	SubmissionCount int        `json:"submission_count"`
	ProblemCount    int        `json:"problem_count"`
	ContestCount    int        `json:"contest_count"`
}

type UpdateUserRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

type UpdateUserBanRequest struct {
	IsBanned bool   `json:"is_banned"`
	Reason   string `json:"reason"`
}

type AuditLogEntry struct {
	ID         int                    `json:"id"`
	UserID     int                    `json:"user_id"`
	Username   string                 `json:"username"`
	Action     string                 `json:"action"`
	EntityType string                 `json:"entity_type"`
	EntityID   int                    `json:"entity_id"`
	Details    map[string]interface{} `json:"details"`
	IPAddress  string                 `json:"ip_address"`
	CreatedAt  time.Time              `json:"created_at"`
}

// ──────────────────────────────────────────────────────────
// Handlers
// ──────────────────────────────────────────────────────────

// AdminListUsers lists all users with pagination and search.
func (h *Handler) AdminListUsers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	limit := 20
	offset := (page - 1) * limit

	search := strings.TrimSpace(c.Query("search"))
	roleFilter := c.Query("role")
	ctx := context.Background()

	query := `SELECT id, username, email, role, rating, is_banned, banned_at, banned_by, ban_reason, created_at
	           FROM app.users WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM app.users WHERE 1=1`
	args := []interface{}{}
	countArgs := []interface{}{}
	argIdx := 1

	if search != "" {
		searchTerm := "%" + strings.ToLower(search) + "%"
		if searchID, err := strconv.Atoi(search); err == nil {
			clause := ` AND (id = $` + strconv.Itoa(argIdx) + ` OR LOWER(username) LIKE $` + strconv.Itoa(argIdx+1) + ` OR LOWER(email) LIKE $` + strconv.Itoa(argIdx+1) + `)`
			query += clause
			countQuery += clause
			args = append(args, searchID, searchTerm)
			countArgs = append(countArgs, searchID, searchTerm)
			argIdx += 2
		} else {
			clause := ` AND (LOWER(username) LIKE $` + strconv.Itoa(argIdx) + ` OR LOWER(email) LIKE $` + strconv.Itoa(argIdx) + `)`
			query += clause
			countQuery += clause
			args = append(args, searchTerm)
			countArgs = append(countArgs, searchTerm)
			argIdx++
		}
	}
	if roleFilter != "" {
		clause := ` AND role = $` + strconv.Itoa(argIdx)
		query += clause
		countQuery += clause
		args = append(args, roleFilter)
		countArgs = append(countArgs, roleFilter)
		argIdx++
	}

	var total int
	_ = h.DB.QueryRow(ctx, countQuery, countArgs...).Scan(&total)

	query += ` ORDER BY id LIMIT $` + strconv.Itoa(argIdx) + ` OFFSET $` + strconv.Itoa(argIdx+1)
	args = append(args, limit, offset)

	rows, err := h.DB.Query(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	users := make([]AdminUserInfo, 0)
	for rows.Next() {
		var u AdminUserInfo
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.Rating,
			&u.IsBanned, &u.BannedAt, &u.BannedBy, &u.BanReason, &u.CreatedAt); err != nil {
			continue
		}
		users = append(users, u)
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  users,
		"total": total,
		"page":  page,
		"pages": (total + limit - 1) / limit,
	})
}

// AdminGetUser returns user detail with activity summary.
func (h *Handler) AdminGetUser(c *gin.Context) {
	targetID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	ctx := context.Background()
	var u AdminUserDetail
	err = h.DB.QueryRow(ctx,
		`SELECT id, username, email, role, rating, is_banned, banned_at, banned_by, ban_reason, created_at
		   FROM app.users
		  WHERE id = $1`,
		targetID,
	).Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.Rating,
		&u.IsBanned, &u.BannedAt, &u.BannedBy, &u.BanReason, &u.CreatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// Activity counts
	_ = h.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM app.submissions WHERE user_id = $1`, targetID,
	).Scan(&u.SubmissionCount)
	_ = h.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM app.problems WHERE created_by = $1`, targetID,
	).Scan(&u.ProblemCount)
	_ = h.DB.QueryRow(ctx,
		`SELECT COUNT(*) FROM app.contest_participants WHERE user_id = $1`, targetID,
	).Scan(&u.ContestCount)

	c.JSON(http.StatusOK, u)
}

// AdminUpdateUserRole promotes or demotes a user.
func (h *Handler) AdminUpdateUserRole(c *gin.Context) {
	targetID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	var req UpdateUserRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	validRoles := map[string]bool{"user": true, "tester": true, "setter": true, "admin": true}
	if !validRoles[req.Role] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role. Must be: user, tester, setter, or admin"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)

	// Prevent self-demotion
	if targetID == uid && req.Role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot demote yourself"})
		return
	}

	ctx := context.Background()
	_, err = h.DB.Exec(ctx, `UPDATE app.users SET role = $1 WHERE id = $2`, req.Role, targetID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update role"})
		return
	}
	h.invalidateUserCache(targetID)

	h.logAudit(uid, "user.update_role", "user", targetID, map[string]interface{}{
		"new_role": req.Role,
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"message": "Role updated"})
}

// AdminUpdateUserBan bans or unbans a user account.
func (h *Handler) AdminUpdateUserBan(c *gin.Context) {
	targetID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	var req UpdateUserBanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	if targetID == uid && req.IsBanned {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot ban yourself"})
		return
	}

	ctx := context.Background()
	var exists bool
	if err := h.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM app.users WHERE id = $1)`, targetID,
	).Scan(&exists); err != nil || !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	reason := strings.TrimSpace(req.Reason)
	if req.IsBanned {
		_, err = h.DB.Exec(ctx,
			`UPDATE app.users
			    SET is_banned = TRUE,
			        banned_at = NOW(),
			        banned_by = $1,
			        ban_reason = $2
			  WHERE id = $3`,
			uid, reason, targetID,
		)
	} else {
		_, err = h.DB.Exec(ctx,
			`UPDATE app.users
			    SET is_banned = FALSE,
			        banned_at = NULL,
			        banned_by = NULL,
			        ban_reason = ''
			  WHERE id = $1`,
			targetID,
		)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update ban status"})
		return
	}
	h.invalidateUserCache(targetID)

	action := "user.unban"
	if req.IsBanned {
		action = "user.ban"
	}
	h.logAudit(uid, action, "user", targetID, map[string]interface{}{
		"is_banned": req.IsBanned,
		"reason":    reason,
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{
		"message":   "Ban status updated",
		"is_banned": req.IsBanned,
	})
}

// AdminGetAuditLog returns paginated audit log entries.
func (h *Handler) AdminGetAuditLog(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	limit := 50
	offset := (page - 1) * limit

	userFilter := c.Query("user_id")
	actionFilter := c.Query("action")
	entityFilter := c.Query("entity_type")
	ctx := context.Background()

	query := `SELECT a.id, a.user_id, u.username, a.action, a.entity_type, a.entity_id,
	                  a.details, a.ip_address, a.created_at
	           FROM app.admin_audit_log a
	           JOIN app.users u ON u.id = a.user_id
	           WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM app.admin_audit_log a WHERE 1=1`
	args := []interface{}{}
	countArgs := []interface{}{}
	argIdx := 1

	if userFilter != "" {
		uid, _ := strconv.Atoi(userFilter)
		clause := ` AND a.user_id = $` + strconv.Itoa(argIdx)
		query += clause
		countQuery += clause
		args = append(args, uid)
		countArgs = append(countArgs, uid)
		argIdx++
	}
	if actionFilter != "" {
		clause := ` AND a.action LIKE $` + strconv.Itoa(argIdx)
		query += clause
		countQuery += clause
		args = append(args, "%"+actionFilter+"%")
		countArgs = append(countArgs, "%"+actionFilter+"%")
		argIdx++
	}
	if entityFilter != "" {
		clause := ` AND a.entity_type = $` + strconv.Itoa(argIdx)
		query += clause
		countQuery += clause
		args = append(args, entityFilter)
		countArgs = append(countArgs, entityFilter)
		argIdx++
	}

	var total int
	_ = h.DB.QueryRow(ctx, countQuery, countArgs...).Scan(&total)

	query += ` ORDER BY a.created_at DESC LIMIT $` + strconv.Itoa(argIdx) + ` OFFSET $` + strconv.Itoa(argIdx+1)
	args = append(args, limit, offset)

	rows, err := h.DB.Query(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	entries := make([]AuditLogEntry, 0)
	for rows.Next() {
		var e AuditLogEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.Username, &e.Action, &e.EntityType,
			&e.EntityID, &e.Details, &e.IPAddress, &e.CreatedAt); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  entries,
		"total": total,
		"page":  page,
		"pages": (total + limit - 1) / limit,
	})
}
