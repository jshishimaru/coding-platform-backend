package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/coding-platform/backend/internal/models"
)

// ──────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────

// isGroupAdmin returns true if the user is a site admin OR a group admin.
func (h *Handler) isGroupAdmin(ctx context.Context, groupID, userID int, siteRole string) bool {
	if siteRole == "admin" {
		return true
	}
	var memberRole string
	err := h.DB.QueryRow(ctx,
		`SELECT role FROM app.group_members WHERE group_id = $1 AND user_id = $2`,
		groupID, userID,
	).Scan(&memberRole)
	if err != nil {
		return false
	}
	return memberRole == "admin"
}

// isGroupMember returns true if the user belongs to the group (member or admin).
// Site admins are treated as implicit members.
func (h *Handler) isGroupMember(ctx context.Context, groupID, userID int, siteRole string) bool {
	if siteRole == "admin" {
		return true
	}
	var exists bool
	_ = h.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM app.group_members WHERE group_id = $1 AND user_id = $2)`,
		groupID, userID,
	).Scan(&exists)
	return exists
}

// ──────────────────────────────────────────────────────────────
// Student / shared endpoints
// ──────────────────────────────────────────────────────────────

// ListGroups returns all groups. Supports ?search=&member=me.
// For every group we include member_count and the caller's membership info.
func (h *Handler) ListGroups(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid, _ := userID.(int)

	search := strings.TrimSpace(c.Query("search"))
	onlyMine := c.Query("member") == "me"
	ctx := context.Background()

	args := []interface{}{uid}
	query := `
		SELECT g.id, g.name, g.description, g.created_by, g.created_at,
		       (SELECT COUNT(*) FROM app.group_members gm WHERE gm.group_id = g.id) AS member_count,
		       COALESCE((SELECT gm.role FROM app.group_members gm WHERE gm.group_id = g.id AND gm.user_id = $1), '') AS my_role
		FROM app.groups g
		WHERE 1=1`
	argIdx := 2

	if search != "" {
		query += " AND LOWER(g.name) LIKE $" + strconv.Itoa(argIdx)
		args = append(args, "%"+strings.ToLower(search)+"%")
		argIdx++
	}
	if onlyMine {
		query += " AND EXISTS (SELECT 1 FROM app.group_members gm WHERE gm.group_id = g.id AND gm.user_id = $1)"
	}
	query += " ORDER BY g.name"

	rows, err := h.DB.Query(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	groups := make([]models.Group, 0)
	for rows.Next() {
		var g models.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedBy, &g.CreatedAt,
			&g.MemberCount, &g.MyRole); err != nil {
			continue
		}
		g.IsMember = g.MyRole != ""
		groups = append(groups, g)
	}

	c.JSON(http.StatusOK, gin.H{"groups": groups})
}

// GetGroup returns a single group. Non-members only see a minimal view.
func (h *Handler) GetGroup(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	userID, _ := c.Get("userID")
	uid, _ := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	ctx := context.Background()

	var g models.Group
	err = h.DB.QueryRow(ctx,
		`SELECT g.id, g.name, g.description, g.created_by, g.created_at,
		        (SELECT COUNT(*) FROM app.group_members WHERE group_id = g.id),
		        COALESCE((SELECT gm.role FROM app.group_members gm WHERE gm.group_id = g.id AND gm.user_id = $1), '')
		 FROM app.groups g WHERE g.id = $2`,
		uid, groupID,
	).Scan(&g.ID, &g.Name, &g.Description, &g.CreatedBy, &g.CreatedAt, &g.MemberCount, &g.MyRole)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
		return
	}
	g.IsMember = g.MyRole != ""

	// Members list — visible to members + site admins
	members := make([]models.GroupMember, 0)
	if g.IsMember || roleStr == "admin" {
		mRows, err := h.DB.Query(ctx,
			`SELECT gm.group_id, gm.user_id, u.username, gm.role, gm.joined_at
			 FROM app.group_members gm
			 JOIN app.users u ON u.id = gm.user_id
			 WHERE gm.group_id = $1
			 ORDER BY gm.role DESC, u.username`, groupID)
		if err == nil {
			defer mRows.Close()
			for mRows.Next() {
				var m models.GroupMember
				if err := mRows.Scan(&m.GroupID, &m.UserID, &m.Username, &m.Role, &m.JoinedAt); err == nil {
					members = append(members, m)
				}
			}
		}
	}

	// Is there a pending request from the current user?
	var pendingRequest bool
	_ = h.DB.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM app.group_join_requests
			WHERE group_id = $1 AND user_id = $2 AND status = 'pending')`,
		groupID, uid,
	).Scan(&pendingRequest)

	c.JSON(http.StatusOK, gin.H{
		"group":           g,
		"members":         members,
		"pending_request": pendingRequest,
	})
}

// RequestJoinGroup creates a pending join request for the current user.
type RequestJoinGroupRequest struct {
	Message string `json:"message"`
}

func (h *Handler) RequestJoinGroup(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	var req RequestJoinGroupRequest
	_ = c.ShouldBindJSON(&req)

	userID, _ := c.Get("userID")
	uid, _ := userID.(int)

	ctx := context.Background()

	// Verify group exists
	var exists bool
	_ = h.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM app.groups WHERE id = $1)`, groupID,
	).Scan(&exists)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
		return
	}

	// If already a member, error
	var alreadyMember bool
	_ = h.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM app.group_members WHERE group_id = $1 AND user_id = $2)`,
		groupID, uid,
	).Scan(&alreadyMember)
	if alreadyMember {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You are already a member of this group"})
		return
	}

	// Insert request; the partial unique index prevents duplicate pending requests.
	var reqID int
	err = h.DB.QueryRow(ctx,
		`INSERT INTO app.group_join_requests (group_id, user_id, status, message)
		 VALUES ($1, $2, 'pending', $3)
		 RETURNING id`,
		groupID, uid, req.Message,
	).Scan(&reqID)
	if err != nil {
		if strings.Contains(err.Error(), "idx_gjr_one_pending") {
			c.JSON(http.StatusConflict, gin.H{"error": "You already have a pending request for this group"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create join request"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": reqID, "message": "Join request submitted"})
}

// CancelJoinRequest lets a user withdraw their own pending request.
func (h *Handler) CancelJoinRequest(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	userID, _ := c.Get("userID")
	uid, _ := userID.(int)

	_, err = h.DB.Exec(context.Background(),
		`DELETE FROM app.group_join_requests
		 WHERE group_id = $1 AND user_id = $2 AND status = 'pending'`,
		groupID, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to cancel"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Request cancelled"})
}

// LeaveGroup allows a user to leave a group they are a member of. Group admins who
// are the last admin in the group cannot leave.
func (h *Handler) LeaveGroup(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	userID, _ := c.Get("userID")
	uid, _ := userID.(int)

	ctx := context.Background()

	// Ensure user is a member
	var myRole string
	err = h.DB.QueryRow(ctx,
		`SELECT role FROM app.group_members WHERE group_id = $1 AND user_id = $2`,
		groupID, uid,
	).Scan(&myRole)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You are not a member of this group"})
		return
	}

	// If sole admin, refuse
	if myRole == "admin" {
		var adminCount int
		_ = h.DB.QueryRow(ctx,
			`SELECT COUNT(*) FROM app.group_members WHERE group_id = $1 AND role = 'admin'`, groupID,
		).Scan(&adminCount)
		if adminCount <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "You are the only group admin. Promote another admin before leaving."})
			return
		}
	}

	_, err = h.DB.Exec(ctx,
		`DELETE FROM app.group_members WHERE group_id = $1 AND user_id = $2`, groupID, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to leave"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Left group"})
}

// ListMyJoinRequests returns the current user's pending/decided requests.
func (h *Handler) ListMyJoinRequests(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid, _ := userID.(int)

	rows, err := h.DB.Query(context.Background(),
		`SELECT r.id, r.group_id, g.name, r.user_id, r.status, r.message,
		        r.decided_by, r.decided_at, r.created_at
		 FROM app.group_join_requests r
		 JOIN app.groups g ON g.id = r.group_id
		 WHERE r.user_id = $1
		 ORDER BY r.created_at DESC`, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	requests := make([]models.GroupJoinRequest, 0)
	for rows.Next() {
		var r models.GroupJoinRequest
		if err := rows.Scan(&r.ID, &r.GroupID, &r.GroupName, &r.UserID,
			&r.Status, &r.Message, &r.DecidedBy, &r.DecidedAt, &r.CreatedAt); err == nil {
			requests = append(requests, r)
		}
	}
	c.JSON(http.StatusOK, gin.H{"requests": requests})
}

// ──────────────────────────────────────────────────────────────
// Admin / group-admin endpoints
// ──────────────────────────────────────────────────────────────

type CreateGroupRequest struct {
	Name        string `json:"name" binding:"required,min=2,max=100"`
	Description string `json:"description"`
	// Optional: existing user IDs to add as group admins on creation
	AdminUserIDs []int `json:"admin_user_ids"`
}

// AdminCreateGroup creates a new group. Site-admin only.
func (h *Handler) AdminCreateGroup(c *gin.Context) {
	var req CreateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)

	ctx := context.Background()
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction"})
		return
	}
	defer tx.Rollback(ctx)

	var groupID int
	err = tx.QueryRow(ctx,
		`INSERT INTO app.groups (name, description, created_by)
		 VALUES ($1, $2, $3) RETURNING id`,
		req.Name, req.Description, uid,
	).Scan(&groupID)
	if err != nil {
		if strings.Contains(err.Error(), "groups_name_key") {
			c.JSON(http.StatusConflict, gin.H{"error": "A group with this name already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create group"})
		return
	}

	// The creator (site admin) is always added as a group admin.
	_, _ = tx.Exec(ctx,
		`INSERT INTO app.group_members (group_id, user_id, role)
		 VALUES ($1, $2, 'admin') ON CONFLICT DO NOTHING`,
		groupID, uid)

	// Additional admins
	for _, adminID := range req.AdminUserIDs {
		if adminID == uid {
			continue
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO app.group_members (group_id, user_id, role)
			 VALUES ($1, $2, 'admin')
			 ON CONFLICT (group_id, user_id) DO UPDATE SET role = 'admin'`,
			groupID, adminID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add group admin"})
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	h.logAudit(uid, "group.create", "group", groupID, map[string]interface{}{
		"name": req.Name,
	}, c.ClientIP())

	c.JSON(http.StatusCreated, gin.H{"id": groupID, "message": "Group created"})
}

type UpdateGroupRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// AdminUpdateGroup updates name/description. Site admin or group admin.
func (h *Handler) AdminUpdateGroup(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	var req UpdateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	ctx := context.Background()
	if !h.isGroupAdmin(ctx, groupID, uid, roleStr) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only group admins can edit the group"})
		return
	}

	updates := []string{}
	args := []interface{}{}
	argIdx := 1
	if req.Name != nil {
		updates = append(updates, "name = $"+strconv.Itoa(argIdx))
		args = append(args, *req.Name)
		argIdx++
	}
	if req.Description != nil {
		updates = append(updates, "description = $"+strconv.Itoa(argIdx))
		args = append(args, *req.Description)
		argIdx++
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No fields to update"})
		return
	}

	query := "UPDATE app.groups SET " + strings.Join(updates, ", ") + " WHERE id = $" + strconv.Itoa(argIdx)
	args = append(args, groupID)
	if _, err := h.DB.Exec(ctx, query, args...); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update group"})
		return
	}

	h.logAudit(uid, "group.update", "group", groupID, nil, c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "Group updated"})
}

// AdminDeleteGroup removes a group (site admin only).
func (h *Handler) AdminDeleteGroup(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	if _, err := h.DB.Exec(context.Background(),
		`DELETE FROM app.groups WHERE id = $1`, groupID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete group"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	h.logAudit(uid, "group.delete", "group", groupID, nil, c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "Group deleted"})
}

// AdminListJoinRequests lists pending requests for a group. Group admin only.
func (h *Handler) AdminListJoinRequests(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	ctx := context.Background()
	if !h.isGroupAdmin(ctx, groupID, uid, roleStr) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only group admins can view join requests"})
		return
	}

	status := c.DefaultQuery("status", "pending")

	rows, err := h.DB.Query(ctx,
		`SELECT r.id, r.group_id, r.user_id, u.username, r.status, r.message,
		        r.decided_by, r.decided_at, r.created_at
		 FROM app.group_join_requests r
		 JOIN app.users u ON u.id = r.user_id
		 WHERE r.group_id = $1 AND r.status = $2
		 ORDER BY r.created_at DESC`, groupID, status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	requests := make([]models.GroupJoinRequest, 0)
	for rows.Next() {
		var r models.GroupJoinRequest
		if err := rows.Scan(&r.ID, &r.GroupID, &r.UserID, &r.Username,
			&r.Status, &r.Message, &r.DecidedBy, &r.DecidedAt, &r.CreatedAt); err == nil {
			requests = append(requests, r)
		}
	}
	c.JSON(http.StatusOK, gin.H{"requests": requests})
}

type DecideRequest struct {
	Decision string `json:"decision" binding:"required"` // 'approved' or 'rejected'
}

// AdminDecideJoinRequest approves or rejects a pending request.
func (h *Handler) AdminDecideJoinRequest(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}
	reqID, err := strconv.Atoi(c.Param("reqId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request ID"})
		return
	}

	var body DecideRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Decision != "approved" && body.Decision != "rejected" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "decision must be 'approved' or 'rejected'"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	ctx := context.Background()
	if !h.isGroupAdmin(ctx, groupID, uid, roleStr) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only group admins can decide requests"})
		return
	}

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to begin transaction"})
		return
	}
	defer tx.Rollback(ctx)

	// Lock & load the request
	var requesterID int
	var status string
	err = tx.QueryRow(ctx,
		`SELECT user_id, status FROM app.group_join_requests
		 WHERE id = $1 AND group_id = $2
		 FOR UPDATE`, reqID, groupID,
	).Scan(&requesterID, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Request not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	if status != "pending" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Request is not pending"})
		return
	}

	_, err = tx.Exec(ctx,
		`UPDATE app.group_join_requests
		 SET status = $1, decided_by = $2, decided_at = NOW()
		 WHERE id = $3`, body.Decision, uid, reqID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update"})
		return
	}

	if body.Decision == "approved" {
		_, err = tx.Exec(ctx,
			`INSERT INTO app.group_members (group_id, user_id, role)
			 VALUES ($1, $2, 'member') ON CONFLICT DO NOTHING`,
			groupID, requesterID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add member"})
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	h.logAudit(uid, "group."+body.Decision, "group", groupID, map[string]interface{}{
		"request_id": reqID, "requester_id": requesterID,
	}, c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"message": "Request " + body.Decision})
}

// ── Member management ─────────────────────────────────────────

type AddMemberRequest struct {
	UserID int    `json:"user_id" binding:"required"`
	Role   string `json:"role"`
}

func (h *Handler) AdminAddGroupMember(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	var req AddMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Role == "" {
		req.Role = "member"
	}
	if req.Role != "member" && req.Role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role must be 'member' or 'admin'"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	ctx := context.Background()
	if !h.isGroupAdmin(ctx, groupID, uid, roleStr) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only group admins can add members"})
		return
	}

	_, err = h.DB.Exec(ctx,
		`INSERT INTO app.group_members (group_id, user_id, role)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (group_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		groupID, req.UserID, req.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add member"})
		return
	}

	h.logAudit(uid, "group.add_member", "group", groupID, map[string]interface{}{
		"user_id": req.UserID, "role": req.Role,
	}, c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "Member added"})
}

type UpdateMemberRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

func (h *Handler) AdminUpdateGroupMember(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}
	targetID, err := strconv.Atoi(c.Param("userId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	var req UpdateMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Role != "member" && req.Role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role must be 'member' or 'admin'"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	ctx := context.Background()
	if !h.isGroupAdmin(ctx, groupID, uid, roleStr) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only group admins can change roles"})
		return
	}

	// If demoting self, ensure there's another admin
	if targetID == uid && req.Role != "admin" {
		var adminCount int
		_ = h.DB.QueryRow(ctx,
			`SELECT COUNT(*) FROM app.group_members WHERE group_id = $1 AND role = 'admin'`, groupID,
		).Scan(&adminCount)
		if adminCount <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot demote the only admin"})
			return
		}
	}

	_, err = h.DB.Exec(ctx,
		`UPDATE app.group_members SET role = $1 WHERE group_id = $2 AND user_id = $3`,
		req.Role, groupID, targetID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update"})
		return
	}

	h.logAudit(uid, "group.update_member", "group", groupID, map[string]interface{}{
		"user_id": targetID, "role": req.Role,
	}, c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "Member updated"})
}

func (h *Handler) AdminRemoveGroupMember(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}
	targetID, err := strconv.Atoi(c.Param("userId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	ctx := context.Background()
	if !h.isGroupAdmin(ctx, groupID, uid, roleStr) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only group admins can remove members"})
		return
	}

	// Prevent removing the sole admin
	var targetRole string
	_ = h.DB.QueryRow(ctx,
		`SELECT role FROM app.group_members WHERE group_id = $1 AND user_id = $2`,
		groupID, targetID,
	).Scan(&targetRole)
	if targetRole == "admin" {
		var adminCount int
		_ = h.DB.QueryRow(ctx,
			`SELECT COUNT(*) FROM app.group_members WHERE group_id = $1 AND role = 'admin'`, groupID,
		).Scan(&adminCount)
		if adminCount <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot remove the only admin"})
			return
		}
	}

	_, err = h.DB.Exec(ctx,
		`DELETE FROM app.group_members WHERE group_id = $1 AND user_id = $2`,
		groupID, targetID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove member"})
		return
	}

	h.logAudit(uid, "group.remove_member", "group", groupID, map[string]interface{}{
		"user_id": targetID,
	}, c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "Member removed"})
}
