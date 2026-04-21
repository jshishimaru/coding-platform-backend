package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/coding-platform/backend/internal/models"
)

// ──────────────────────────────────────────────────────────────
// Types
// ──────────────────────────────────────────────────────────────

// Allowed event types we accept from the client.
var allowedProctorEvents = map[string]bool{
	"fs_exit":        true, // exit from fullscreen
	"fs_enter":       true, // entered fullscreen (informational)
	"tab_visibility": true, // tab hidden / visible
	"window_blur":    true, // window lost focus
	"window_focus":   true, // window regained focus
	"right_click":    true, // context menu attempt
	"devtools":       true, // devtools suspected open
	"paste":          true, // large paste detected
	"copy":           true, // copy action detected
	"unload":         true, // page close / reload during contest
	"session_start":  true, // client registered start
}

type RecordProctorEventRequest struct {
	EventType string                 `json:"event_type" binding:"required"`
	Details   map[string]interface{} `json:"details"`
}

// ──────────────────────────────────────────────────────────────
// Student endpoint
// ──────────────────────────────────────────────────────────────

// RecordProctorEvent accepts a monitoring event for a proctored contest.
// No hard enforcement — just logging for admin review.
func (h *Handler) RecordProctorEvent(c *gin.Context) {
	contestID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	var req RecordProctorEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !allowedProctorEvents[req.EventType] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown event_type"})
		return
	}

	userID, _ := c.Get("userID")
	uid, _ := userID.(int)

	ctx := context.Background()

	// Only accept events if the contest is proctored (silently ignore otherwise)
	var proctored bool
	err = h.DB.QueryRow(ctx,
		`SELECT proctored FROM app.contests WHERE id = $1`, contestID,
	).Scan(&proctored)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contest not found"})
		return
	}
	if !proctored {
		c.JSON(http.StatusOK, gin.H{"message": "Contest is not proctored; event ignored"})
		return
	}

	detailsJSON, _ := json.Marshal(req.Details)
	if len(detailsJSON) == 0 {
		detailsJSON = []byte("{}")
	}

	_, err = h.DB.Exec(ctx,
		`INSERT INTO app.proctor_events (contest_id, user_id, event_type, details)
		 VALUES ($1, $2, $3, $4::jsonb)`,
		contestID, uid, req.EventType, string(detailsJSON),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record event"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Event recorded"})
}

// ──────────────────────────────────────────────────────────────
// Admin endpoints
// ──────────────────────────────────────────────────────────────

// AdminListProctorEvents returns proctor events for a contest.
// Optional filters: ?user_id=<n>&event_type=<s>&page=<n>
func (h *Handler) AdminListProctorEvents(c *gin.Context) {
	contestID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	canManageContest, err := h.canManageContest(context.Background(), uid, roleStr, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate contest permissions"})
		return
	}
	if !canManageContest {
		c.JSON(http.StatusForbidden, gin.H{"error": "You can only access proctoring data for managed group contests"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	limit := 100
	offset := (page - 1) * limit

	userFilter := c.Query("user_id")
	eventFilter := c.Query("event_type")

	args := []interface{}{contestID}
	query := `SELECT e.id, e.contest_id, e.user_id, u.username, e.event_type, e.details, e.created_at
	          FROM app.proctor_events e
	          JOIN app.users u ON u.id = e.user_id
	          WHERE e.contest_id = $1`
	countQuery := `SELECT COUNT(*) FROM app.proctor_events e WHERE e.contest_id = $1`
	countArgs := []interface{}{contestID}
	argIdx := 2

	if userFilter != "" {
		uid, _ := strconv.Atoi(userFilter)
		clause := ` AND e.user_id = $` + strconv.Itoa(argIdx)
		query += clause
		countQuery += clause
		args = append(args, uid)
		countArgs = append(countArgs, uid)
		argIdx++
	}
	if eventFilter != "" {
		clause := ` AND e.event_type = $` + strconv.Itoa(argIdx)
		query += clause
		countQuery += clause
		args = append(args, eventFilter)
		countArgs = append(countArgs, eventFilter)
		argIdx++
	}

	var total int
	_ = h.DB.QueryRow(context.Background(), countQuery, countArgs...).Scan(&total)

	query += ` ORDER BY e.created_at DESC
	           LIMIT $` + strconv.Itoa(argIdx) + ` OFFSET $` + strconv.Itoa(argIdx+1)
	args = append(args, limit, offset)

	rows, err := h.DB.Query(context.Background(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	events := make([]models.ProctorEvent, 0)
	for rows.Next() {
		var e models.ProctorEvent
		if err := rows.Scan(&e.ID, &e.ContestID, &e.UserID, &e.Username,
			&e.EventType, &e.Details, &e.CreatedAt); err == nil {
			events = append(events, e)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"events": events,
		"total":  total,
		"page":   page,
		"pages":  (total + limit - 1) / limit,
	})
}

// AdminProctorSummary returns per-user event counts for a contest.
func (h *Handler) AdminProctorSummary(c *gin.Context) {
	contestID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	canManageContest, err := h.canManageContest(context.Background(), uid, roleStr, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate contest permissions"})
		return
	}
	if !canManageContest {
		c.JSON(http.StatusForbidden, gin.H{"error": "You can only access proctoring data for managed group contests"})
		return
	}

	rows, err := h.DB.Query(context.Background(),
		`SELECT u.id, u.username,
		        COUNT(*) FILTER (WHERE e.event_type = 'fs_exit')        AS fs_exits,
		        COUNT(*) FILTER (WHERE e.event_type = 'tab_visibility') AS tab_switches,
		        COUNT(*) FILTER (WHERE e.event_type = 'window_blur')    AS blurs,
		        COUNT(*) FILTER (WHERE e.event_type = 'right_click')    AS right_clicks,
		        COUNT(*) FILTER (WHERE e.event_type = 'devtools')       AS devtools,
		        COUNT(*)                                                AS total_events,
		        MAX(e.created_at)                                       AS last_event_at
		 FROM app.proctor_events e
		 JOIN app.users u ON u.id = e.user_id
		 WHERE e.contest_id = $1
		 GROUP BY u.id, u.username
		 ORDER BY total_events DESC`, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	type row struct {
		UserID      int        `json:"user_id"`
		Username    string     `json:"username"`
		FsExits     int        `json:"fs_exits"`
		TabSwitches int        `json:"tab_switches"`
		Blurs       int        `json:"blurs"`
		RightClicks int        `json:"right_clicks"`
		Devtools    int        `json:"devtools"`
		TotalEvents int        `json:"total_events"`
		LastEventAt *time.Time `json:"last_event_at,omitempty"`
	}

	result := make([]row, 0)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.UserID, &r.Username, &r.FsExits, &r.TabSwitches,
			&r.Blurs, &r.RightClicks, &r.Devtools, &r.TotalEvents, &r.LastEventAt); err == nil {
			result = append(result, r)
		}
	}

	c.JSON(http.StatusOK, gin.H{"summary": result})
}
