package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/coding-platform/backend/internal/sandbox"
)

// ──────────────────────────────────────────────────────────────
// Types
// ──────────────────────────────────────────────────────────────

type CreateContestRequest struct {
	Title           string                  `json:"title" binding:"required"`
	Description     string                  `json:"description"`
	StartTime       string                  `json:"start_time" binding:"required"`
	EndTime         string                  `json:"end_time" binding:"required"`
	IsRated         bool                    `json:"is_rated"`
	GroupID         *int                    `json:"group_id"`
	Proctored       bool                    `json:"proctored"`
	GradeVisibility string                  `json:"grade_visibility"`
	Problems        []ContestProblemRequest `json:"problems" binding:"required"`
}

type ContestProblemRequest struct {
	ProblemID    int    `json:"problem_id" binding:"required"`
	Points       int    `json:"points"`
	ProblemOrder int    `json:"problem_order"`
	ScoringMode  string `json:"scoring_mode"` // "all_or_nothing" or "partial"
}

type ContestSummary struct {
	ID              int       `json:"id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	StartTime       time.Time `json:"start_time"`
	EndTime         time.Time `json:"end_time"`
	IsRated         bool      `json:"is_rated"`
	Status          string    `json:"status"`
	Participants    int       `json:"participants"`
	ProblemCount    int       `json:"problem_count"`
	GroupID         *int      `json:"group_id,omitempty"`
	GroupName       string    `json:"group_name,omitempty"`
	Proctored       bool      `json:"proctored"`
	GradeVisibility string    `json:"grade_visibility"`
}

type ContestDetail struct {
	ID              int                    `json:"id"`
	Title           string                 `json:"title"`
	Description     string                 `json:"description"`
	StartTime       time.Time              `json:"start_time"`
	EndTime         time.Time              `json:"end_time"`
	IsRated         bool                   `json:"is_rated"`
	Status          string                 `json:"status"`
	Problems        []ContestProblemDetail `json:"problems"`
	GroupID         *int                   `json:"group_id,omitempty"`
	GroupName       string                 `json:"group_name,omitempty"`
	Proctored       bool                   `json:"proctored"`
	GradeVisibility string                 `json:"grade_visibility"`
}

type ContestProblemDetail struct {
	ProblemID     int      `json:"problem_id"`
	Title         string   `json:"title"`
	Slug          string   `json:"slug"`
	Points        int      `json:"points"`
	ProblemOrder  int      `json:"problem_order"`
	Difficulty    string   `json:"difficulty"`
	TimeLimitMs   int      `json:"time_limit_ms"`
	MemoryLimitMb int      `json:"memory_limit_mb"`
	ProblemType   string   `json:"problem_type"`
	ScoringMode   string   `json:"scoring_mode"`
	Tags          []string `json:"tags"`
}

type LeaderboardEntry struct {
	Rank         int                     `json:"rank"`
	UserID       int                     `json:"user_id"`
	Username     string                  `json:"username"`
	Score        int                     `json:"score"`
	PenaltyTime  int                     `json:"penalty_time"`
	Rating       int                     `json:"rating"`
	RatingBefore *int                    `json:"rating_before,omitempty"`
	RatingChange *int                    `json:"rating_change,omitempty"`
	Solves       []LeaderboardSolveEntry `json:"solves"`
}

type LeaderboardSolveEntry struct {
	ProblemID    int       `json:"problem_id"`
	PointsEarned int       `json:"points_earned"`
	SolvedAt     time.Time `json:"solved_at"`
}

type ContestSubmitRequest struct {
	ProblemID int    `json:"problem_id" binding:"required"`
	Code      string `json:"code" binding:"required"`
	Language  string `json:"language" binding:"required"`
}

type RatingPrediction struct {
	UserID          int    `json:"user_id"`
	Username        string `json:"username"`
	CurrentRating   int    `json:"current_rating"`
	PredictedRating int    `json:"predicted_rating"`
	PredictedChange int    `json:"predicted_change"`
	CurrentRank     int    `json:"current_rank"`
}

// ──────────────────────────────────────────────────────────────
// In-memory rating predictor hub (WebSocket-like via SSE)
// ──────────────────────────────────────────────────────────────

type ratingPredictorHub struct {
	mu      sync.RWMutex
	clients map[int]map[chan []byte]bool // contestID -> set of channels
}

var predictorHub = &ratingPredictorHub{
	clients: make(map[int]map[chan []byte]bool),
}

func (hub *ratingPredictorHub) subscribe(contestID int) chan []byte {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.clients[contestID] == nil {
		hub.clients[contestID] = make(map[chan []byte]bool)
	}
	ch := make(chan []byte, 16)
	hub.clients[contestID][ch] = true
	return ch
}

func (hub *ratingPredictorHub) unsubscribe(contestID int, ch chan []byte) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if m, ok := hub.clients[contestID]; ok {
		delete(m, ch)
		close(ch)
	}
}

func (hub *ratingPredictorHub) broadcast(contestID int, data []byte) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for ch := range hub.clients[contestID] {
		select {
		case ch <- data:
		default: // drop if client is too slow
		}
	}
}

// ──────────────────────────────────────────────────────────────
// ELO Rating System (Codeforces-inspired)
// ──────────────────────────────────────────────────────────────

// expectedScore returns P(player with ratingA beats ratingB).
func expectedScore(ratingA, ratingB float64) float64 {
	return 1.0 / (1.0 + math.Pow(10.0, (ratingB-ratingA)/400.0))
}

// calculateELO computes new ratings given a sorted list of (userID, oldRating)
// ordered by rank (best first). Returns map[userID]newRating.
func calculateELO(participants []struct {
	UserID int
	Rating int
	Rank   int
}) map[int]int {
	n := len(participants)
	if n == 0 {
		return nil
	}

	result := make(map[int]int, n)

	for i := 0; i < n; i++ {
		ri := float64(participants[i].Rating)

		// Compute expected rank
		expectedRank := 0.0
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			expectedRank += expectedScore(float64(participants[j].Rating), ri)
		}
		expectedRank += 1.0 // 1-indexed

		actualRank := float64(participants[i].Rank)

		// K-factor based on rating
		k := 40.0
		if ri >= 1400 {
			k = 30.0
		}
		if ri >= 1800 {
			k = 20.0
		}
		if ri >= 2200 {
			k = 15.0
		}

		// New rating: positive when actual rank < expected rank (did better)
		delta := k * (expectedRank - actualRank) / float64(n)
		newRating := int(math.Round(ri + delta))
		if newRating < 0 {
			newRating = 0
		}
		result[participants[i].UserID] = newRating
	}

	return result
}

// predictRatings is like calculateELO but returns predictions (doesn't save).
func (h *Handler) predictRatings(contestID int) []RatingPrediction {
	ctx := context.Background()

	// Get current leaderboard
	rows, err := h.DB.Query(ctx,
		`SELECT cp.user_id, u.username, u.rating, cp.score, cp.penalty_time
 FROM app.contest_participants cp
 JOIN app.users u ON u.id = cp.user_id
 WHERE cp.contest_id = $1
 ORDER BY cp.score DESC, cp.penalty_time ASC`, contestID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	type participant struct {
		UserID   int
		Username string
		Rating   int
		Score    int
		Penalty  int
	}
	var parts []participant
	for rows.Next() {
		var p participant
		if err := rows.Scan(&p.UserID, &p.Username, &p.Rating, &p.Score, &p.Penalty); err == nil {
			parts = append(parts, p)
		}
	}

	if len(parts) == 0 {
		return nil
	}

	// Assign ranks (1-indexed, ties get same rank)
	eloInput := make([]struct {
		UserID int
		Rating int
		Rank   int
	}, len(parts))
	for i, p := range parts {
		rank := i + 1
		if i > 0 && parts[i].Score == parts[i-1].Score && parts[i].Penalty == parts[i-1].Penalty {
			rank = eloInput[i-1].Rank
		}
		eloInput[i] = struct {
			UserID int
			Rating int
			Rank   int
		}{p.UserID, p.Rating, rank}
	}

	newRatings := calculateELO(eloInput)

	predictions := make([]RatingPrediction, len(parts))
	for i, p := range parts {
		nr := newRatings[p.UserID]
		predictions[i] = RatingPrediction{
			UserID:          p.UserID,
			Username:        p.Username,
			CurrentRating:   p.Rating,
			PredictedRating: nr,
			PredictedChange: nr - p.Rating,
			CurrentRank:     eloInput[i].Rank,
		}
	}

	return predictions
}

// ──────────────────────────────────────────────────────────────
// Helper: contest status
// ──────────────────────────────────────────────────────────────

func contestStatus(start, end time.Time) string {
	now := time.Now().UTC()
	if now.Before(start) {
		return "upcoming"
	}
	if now.After(end) {
		return "ended"
	}
	return "live"
}

// ──────────────────────────────────────────────────────────────
// Health
// ──────────────────────────────────────────────────────────────

func (h *Handler) ContestsHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"module":    "contests",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// ──────────────────────────────────────────────────────────────
// List Contests
// ──────────────────────────────────────────────────────────────

func (h *Handler) ListContests(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid, _ := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	// Site admins see everything, everyone else only sees:
	//   - global contests (group_id IS NULL), OR
	//   - group contests where they are a member
	query := `
		SELECT c.id, c.title, c.description, c.start_time, c.end_time, c.is_rated,
		       c.group_id, COALESCE(g.name, ''),
		       c.proctored, c.grade_visibility,
		       (SELECT COUNT(*) FROM app.contest_participants WHERE contest_id = c.id),
		       (SELECT COUNT(*) FROM app.contest_problems   WHERE contest_id = c.id)
		FROM app.contests c
		LEFT JOIN app.groups g ON g.id = c.group_id`

	args := []interface{}{}
	if roleStr != "admin" {
		query += `
		WHERE c.group_id IS NULL
		   OR EXISTS (
		       SELECT 1 FROM app.group_members gm
		       WHERE gm.group_id = c.group_id AND gm.user_id = $1
		   )`
		args = append(args, uid)
	}
	query += ` ORDER BY c.start_time DESC`

	rows, err := h.DB.Query(context.Background(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	contests := make([]ContestSummary, 0)
	for rows.Next() {
		var cs ContestSummary
		if err := rows.Scan(&cs.ID, &cs.Title, &cs.Description, &cs.StartTime, &cs.EndTime,
			&cs.IsRated, &cs.GroupID, &cs.GroupName, &cs.Proctored, &cs.GradeVisibility,
			&cs.Participants, &cs.ProblemCount); err != nil {
			continue
		}
		cs.Status = contestStatus(cs.StartTime, cs.EndTime)
		contests = append(contests, cs)
	}

	c.JSON(http.StatusOK, gin.H{"contests": contests})
}

// ──────────────────────────────────────────────────────────────
// Get Contest
// ──────────────────────────────────────────────────────────────

func (h *Handler) GetContest(c *gin.Context) {
	idStr := c.Param("id")
	contestID, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	userID, _ := c.Get("userID")
	uid, _ := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	var cd ContestDetail
	err = h.DB.QueryRow(context.Background(),
		`SELECT c.id, c.title, c.description, c.start_time, c.end_time, c.is_rated,
		        c.group_id, COALESCE(g.name, ''), c.proctored, c.grade_visibility
		 FROM app.contests c
		 LEFT JOIN app.groups g ON g.id = c.group_id
		 WHERE c.id = $1`, contestID,
	).Scan(&cd.ID, &cd.Title, &cd.Description, &cd.StartTime, &cd.EndTime, &cd.IsRated,
		&cd.GroupID, &cd.GroupName, &cd.Proctored, &cd.GradeVisibility)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contest not found"})
		return
	}
	cd.Status = contestStatus(cd.StartTime, cd.EndTime)

	// Enforce group membership for group contests
	if cd.GroupID != nil && roleStr != "admin" {
		if !h.isGroupMember(context.Background(), *cd.GroupID, uid, roleStr) {
			c.JSON(http.StatusForbidden, gin.H{"error": "This contest is restricted to group members"})
			return
		}
	}

	// Fetch problems
	rows, err := h.DB.Query(context.Background(),
		`SELECT cp.problem_id, p.title, p.slug, cp.points, cp.problem_order,
		        p.difficulty, p.time_limit_ms, p.memory_limit_mb,
		        p.problem_type, cp.scoring_mode
		 FROM app.contest_problems cp
		 JOIN app.problems p ON p.id = cp.problem_id
		 WHERE cp.contest_id = $1
		 ORDER BY cp.problem_order`, contestID)
	if err == nil {
		defer rows.Close()
		cd.Problems = make([]ContestProblemDetail, 0)
		for rows.Next() {
			var p ContestProblemDetail
			if err := rows.Scan(&p.ProblemID, &p.Title, &p.Slug, &p.Points, &p.ProblemOrder,
				&p.Difficulty, &p.TimeLimitMs, &p.MemoryLimitMb,
				&p.ProblemType, &p.ScoringMode); err == nil {
				p.Tags = make([]string, 0)
				cd.Problems = append(cd.Problems, p)
			}
		}
	}

	// Fetch tags for contest problems — only if contest is NOT live
	if cd.Status != "live" && len(cd.Problems) > 0 {
		ids := make([]int, len(cd.Problems))
		idxMap := make(map[int]int)
		for i, p := range cd.Problems {
			ids[i] = p.ProblemID
			idxMap[p.ProblemID] = i
		}

		tagRows, err := h.DB.Query(context.Background(),
			`SELECT pt.problem_id, t.name
			 FROM app.problem_tags pt
			 JOIN app.tags t ON t.id = pt.tag_id
			 WHERE pt.problem_id = ANY($1)
			 ORDER BY t.name`, ids)
		if err == nil {
			defer tagRows.Close()
			for tagRows.Next() {
				var pid int
				var name string
				if err := tagRows.Scan(&pid, &name); err == nil {
					if idx, ok := idxMap[pid]; ok {
						cd.Problems[idx].Tags = append(cd.Problems[idx].Tags, name)
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, cd)
}

// ──────────────────────────────────────────────────────────────
// Create Contest (admin/protected)
// ──────────────────────────────────────────────────────────────

func (h *Handler) CreateContest(c *gin.Context) {
	var req CreateContestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	startTime, err := time.Parse(time.RFC3339, req.StartTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid start_time format (use RFC3339)"})
		return
	}
	endTime, err := time.Parse(time.RFC3339, req.EndTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid end_time format (use RFC3339)"})
		return
	}

	userID, _ := c.Get("userID")

	// Group contests are always unrated, regardless of request
	if req.GroupID != nil {
		req.IsRated = false
	}

	gradeVis := req.GradeVisibility
	if gradeVis == "" {
		gradeVis = "private"
	}
	if gradeVis != "private" && gradeVis != "group" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "grade_visibility must be 'private' or 'group'"})
		return
	}

	tx, err := h.DB.Begin(context.Background())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback(context.Background())

	var contestID int
	err = tx.QueryRow(context.Background(),
		`INSERT INTO app.contests (title, description, start_time, end_time, is_rated, created_by,
		                           group_id, proctored, grade_visibility)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		req.Title, req.Description, startTime, endTime, req.IsRated, userID,
		req.GroupID, req.Proctored, gradeVis,
	).Scan(&contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create contest"})
		return
	}

	for i, p := range req.Problems {
		points := p.Points
		if points <= 0 {
			points = 100
		}
		order := p.ProblemOrder
		if order <= 0 {
			order = i + 1
		}
		sm := p.ScoringMode
		if sm == "" {
			sm = "all_or_nothing"
		}
		if sm != "all_or_nothing" && sm != "partial" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "scoring_mode must be 'all_or_nothing' or 'partial'"})
			return
		}
		_, err := tx.Exec(context.Background(),
			`INSERT INTO app.contest_problems (contest_id, problem_id, points, problem_order, scoring_mode)
			 VALUES ($1, $2, $3, $4, $5)`,
			contestID, p.ProblemID, points, order, sm,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add problem to contest"})
			return
		}
	}

	if err := tx.Commit(context.Background()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": contestID, "message": "Contest created"})
}

// ──────────────────────────────────────────────────────────────
// Submit solution for a contest problem
// ──────────────────────────────────────────────────────────────

func (h *Handler) ContestSubmit(c *gin.Context) {
	idStr := c.Param("id")
	contestID, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	var req ContestSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Language != "cpp" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only C++ is supported currently"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	// Verify contest exists and is live; pull group info for membership check
	var startTime, endTime time.Time
	var groupID *int
	err = h.DB.QueryRow(context.Background(),
		`SELECT start_time, end_time, group_id FROM app.contests WHERE id = $1`, contestID,
	).Scan(&startTime, &endTime, &groupID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contest not found"})
		return
	}

	status := contestStatus(startTime, endTime)
	if status != "live" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Contest is not live"})
		return
	}

	// Group-only visibility
	if groupID != nil && !h.isGroupMember(context.Background(), *groupID, uid, roleStr) {
		c.JSON(http.StatusForbidden, gin.H{"error": "You must be a member of this group to submit"})
		return
	}

	// Verify problem is in this contest; also pull problem_type + scoring_mode
	var points, timeLimitMs, memoryLimitMb int
	var checkerCode, slug, problemType, scoringMode string
	err = h.DB.QueryRow(context.Background(),
		`SELECT cp.points, p.time_limit_ms, p.memory_limit_mb, p.checker_code, p.slug,
		        p.problem_type, cp.scoring_mode
		 FROM app.contest_problems cp
		 JOIN app.problems p ON p.id = cp.problem_id
		 WHERE cp.contest_id = $1 AND cp.problem_id = $2`,
		contestID, req.ProblemID,
	).Scan(&points, &timeLimitMs, &memoryLimitMb, &checkerCode, &slug, &problemType, &scoringMode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Problem not in this contest"})
		return
	}

	// Auto-join participant if not already joined (needed for both flows)
	h.DB.Exec(context.Background(),
		`INSERT INTO app.contest_participants (contest_id, user_id, score, penalty_time)
		 VALUES ($1, $2, 0, 0) ON CONFLICT DO NOTHING`,
		contestID, uid,
	)

	// ── Subjective problems: skip judge, record as pending_review ────────
	if problemType == "subjective" {
		var subID int
		var submittedAt time.Time
		err := h.DB.QueryRow(context.Background(),
			`INSERT INTO app.submissions (user_id, problem_id, contest_id, language, source_code,
			                              status, passed_count, total_count)
			 VALUES ($1, $2, $3, $4, $5, 'pending_review', 0, 0)
			 RETURNING id, submitted_at`,
			uid, req.ProblemID, contestID, req.Language, req.Code,
		).Scan(&subID, &submittedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save submission"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"submission": SubmissionResponse{
				ID:          subID,
				ProblemID:   req.ProblemID,
				ProblemSlug: slug,
				Status:      "pending_review",
				Language:    req.Language,
				SubmittedAt: submittedAt,
			},
			"message": "Submission received and awaiting manual review.",
		})
		return
	}

	// Check if already solved — only applies to all-or-nothing standard problems.
	// Partial-scoring problems allow resubmission (we take max).
	if scoringMode != "partial" {
		var alreadySolved bool
		h.DB.QueryRow(context.Background(),
			`SELECT EXISTS(SELECT 1 FROM app.contest_solves WHERE contest_id=$1 AND user_id=$2 AND problem_id=$3)`,
			contestID, uid, req.ProblemID,
		).Scan(&alreadySolved)
		if alreadySolved {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Already solved this problem in this contest"})
			return
		}
	}

	// Fetch test cases
	rows, err := h.DB.Query(context.Background(),
		`SELECT id, input, expected_output, is_sample
 FROM app.test_cases WHERE problem_id = $1 ORDER BY id`, req.ProblemID)
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

	// Save submission
	var runtimeMs, memoryKb *int
	if result.MaxTimeMs > 0 {
		v := int(result.MaxTimeMs)
		runtimeMs = &v
	}
	if result.MaxMemoryKB > 0 {
		v := int(result.MaxMemoryKB)
		memoryKb = &v
	}

	resultJSON, _ := json.Marshal(result)

	var submissionID int
	var submittedAt time.Time
	err = h.DB.QueryRow(context.Background(),
		`INSERT INTO app.submissions (user_id, problem_id, contest_id, language, source_code, status, runtime_ms, memory_kb, passed_count, total_count, result_details)
 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
 RETURNING id, submitted_at`,
		uid, req.ProblemID, contestID, req.Language, req.Code, result.Status,
		runtimeMs, memoryKb, result.PassedCount, result.TotalCount, resultJSON,
	).Scan(&submissionID, &submittedAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save submission: " + err.Error()})
		return
	}

	// Scoring:
	//   all_or_nothing: only AC → full points
	//   partial:        earned = round(points * passed/total), keep the best solve
	earnedPoints := 0
	if scoringMode == "partial" {
		if result.TotalCount > 0 {
			earnedPoints = int(float64(points) * float64(result.PassedCount) / float64(result.TotalCount))
		}
	} else if result.Status == "accepted" {
		earnedPoints = points
	}

	if earnedPoints > 0 || result.Status == "accepted" {
		if scoringMode == "partial" {
			// Upsert — keep the best score for this problem in this contest.
			_, err = h.DB.Exec(context.Background(),
				`INSERT INTO app.contest_solves
				     (contest_id, user_id, problem_id, submission_id, points_earned, solved_at)
				 VALUES ($1, $2, $3, $4, $5, $6)
				 ON CONFLICT (contest_id, user_id, problem_id) DO UPDATE
				 SET points_earned = GREATEST(app.contest_solves.points_earned, EXCLUDED.points_earned),
				     submission_id = CASE
				         WHEN EXCLUDED.points_earned > app.contest_solves.points_earned
				         THEN EXCLUDED.submission_id
				         ELSE app.contest_solves.submission_id
				     END,
				     solved_at = CASE
				         WHEN EXCLUDED.points_earned > app.contest_solves.points_earned
				         THEN EXCLUDED.solved_at
				         ELSE app.contest_solves.solved_at
				     END`,
				contestID, uid, req.ProblemID, submissionID, earnedPoints, submittedAt,
			)
		} else {
			_, err = h.DB.Exec(context.Background(),
				`INSERT INTO app.contest_solves
				     (contest_id, user_id, problem_id, submission_id, points_earned, solved_at)
				 VALUES ($1, $2, $3, $4, $5, $6)
				 ON CONFLICT DO NOTHING`,
				contestID, uid, req.ProblemID, submissionID, earnedPoints, submittedAt,
			)
		}

		if err == nil {
			h.DB.Exec(context.Background(),
				`UPDATE app.contest_participants
				 SET score = (SELECT COALESCE(SUM(points_earned), 0)
				              FROM app.contest_solves
				              WHERE contest_id = $1 AND user_id = $2),
				     penalty_time = (SELECT COALESCE(SUM(EXTRACT(EPOCH FROM (solved_at - $3::timestamptz)) / 60)::int, 0)
				                     FROM app.contest_solves
				                     WHERE contest_id = $1 AND user_id = $2)
				 WHERE contest_id = $1 AND user_id = $2`,
				contestID, uid, startTime,
			)

			go func() {
				predictions := h.predictRatings(contestID)
				if predictions != nil {
					data, _ := json.Marshal(gin.H{"type": "rating_update", "predictions": predictions})
					predictorHub.broadcast(contestID, data)
				}
			}()
		}
	}

	// Strip hidden test case details
	for i := range result.TestCaseResults {
		if !result.TestCaseResults[i].IsSample {
			result.TestCaseResults[i].Stdout = ""
			result.TestCaseResults[i].Stderr = ""
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"submission": SubmissionResponse{
			ID:          submissionID,
			ProblemID:   req.ProblemID,
			ProblemSlug: slug,
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

// ──────────────────────────────────────────────────────────────
// Leaderboard
// ──────────────────────────────────────────────────────────────

func (h *Handler) ContestLeaderboard(c *gin.Context) {
	idStr := c.Param("id")
	contestID, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	userID, _ := c.Get("userID")
	uid, _ := userID.(int)
	role, _ := c.Get("role")
	roleStr, _ := role.(string)

	// Load visibility + group info
	var gradeVisibility string
	var groupID *int
	err = h.DB.QueryRow(context.Background(),
		`SELECT grade_visibility, group_id FROM app.contests WHERE id = $1`, contestID,
	).Scan(&gradeVisibility, &groupID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contest not found"})
		return
	}

	// Group visibility check — non-admins must be members of the group
	if groupID != nil && roleStr != "admin" {
		if !h.isGroupMember(context.Background(), *groupID, uid, roleStr) {
			c.JSON(http.StatusForbidden, gin.H{"error": "This leaderboard is restricted to group members"})
			return
		}
	}

	// Grade visibility:
	//   'private' → non-admins only see their own row
	//   'group'   → all participants see the full leaderboard
	restrictToSelf := false
	if gradeVisibility == "private" && roleStr != "admin" {
		restrictToSelf = true
	}

	query := `SELECT cp.user_id, u.username, cp.score, cp.penalty_time, u.rating,
	                 cp.rating_before, cp.rating_change
	          FROM app.contest_participants cp
	          JOIN app.users u ON u.id = cp.user_id
	          WHERE cp.contest_id = $1`
	args := []interface{}{contestID}
	if restrictToSelf {
		query += ` AND cp.user_id = $2`
		args = append(args, uid)
	}
	query += ` ORDER BY cp.score DESC, cp.penalty_time ASC`

	rows, err := h.DB.Query(context.Background(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	entries := make([]LeaderboardEntry, 0)
	for rows.Next() {
		var e LeaderboardEntry
		if err := rows.Scan(&e.UserID, &e.Username, &e.Score, &e.PenaltyTime,
			&e.Rating, &e.RatingBefore, &e.RatingChange); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	for i := range entries {
		entries[i].Rank = i + 1
		if i > 0 && entries[i].Score == entries[i-1].Score && entries[i].PenaltyTime == entries[i-1].PenaltyTime {
			entries[i].Rank = entries[i-1].Rank
		}
	}

	// Fetch solves (scoped same as leaderboard entries)
	solveQuery := `SELECT user_id, problem_id, points_earned, solved_at
	               FROM app.contest_solves WHERE contest_id = $1`
	solveArgs := []interface{}{contestID}
	if restrictToSelf {
		solveQuery += ` AND user_id = $2`
		solveArgs = append(solveArgs, uid)
	}

	solveRows, err := h.DB.Query(context.Background(), solveQuery, solveArgs...)
	if err == nil {
		defer solveRows.Close()
		solveMap := make(map[int][]LeaderboardSolveEntry)
		for solveRows.Next() {
			var suid, pid, pts int
			var solvedAt time.Time
			if err := solveRows.Scan(&suid, &pid, &pts, &solvedAt); err == nil {
				solveMap[suid] = append(solveMap[suid], LeaderboardSolveEntry{
					ProblemID:    pid,
					PointsEarned: pts,
					SolvedAt:     solvedAt,
				})
			}
		}
		for i := range entries {
			entries[i].Solves = solveMap[entries[i].UserID]
			if entries[i].Solves == nil {
				entries[i].Solves = []LeaderboardSolveEntry{}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"leaderboard":      entries,
		"grade_visibility": gradeVisibility,
	})
}

// ──────────────────────────────────────────────────────────────
// Finalize Contest (apply ELO ratings) - admin endpoint
// ──────────────────────────────────────────────────────────────

func (h *Handler) FinalizeContest(c *gin.Context) {
	idStr := c.Param("id")
	contestID, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	// Check contest is ended and rated
	var endTime time.Time
	var isRated bool
	err = h.DB.QueryRow(context.Background(),
		`SELECT end_time, is_rated FROM app.contests WHERE id = $1`, contestID,
	).Scan(&endTime, &isRated)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contest not found"})
		return
	}
	if !isRated {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Contest is not rated"})
		return
	}
	if time.Now().UTC().Before(endTime) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Contest has not ended yet"})
		return
	}

	// Check not already finalized
	var alreadyFinalized bool
	h.DB.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM app.contest_participants WHERE contest_id=$1 AND rating_after IS NOT NULL LIMIT 1)`,
		contestID,
	).Scan(&alreadyFinalized)
	if alreadyFinalized {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Contest already finalized"})
		return
	}

	// Get participants with ranks
	rows, err := h.DB.Query(context.Background(),
		`SELECT cp.user_id, u.rating, cp.score, cp.penalty_time
 FROM app.contest_participants cp
 JOIN app.users u ON u.id = cp.user_id
 WHERE cp.contest_id = $1
 ORDER BY cp.score DESC, cp.penalty_time ASC`, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	type part struct {
		UserID  int
		Rating  int
		Score   int
		Penalty int
	}
	var parts []part
	for rows.Next() {
		var p part
		if err := rows.Scan(&p.UserID, &p.Rating, &p.Score, &p.Penalty); err == nil {
			parts = append(parts, p)
		}
	}

	if len(parts) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No participants"})
		return
	}

	// Assign ranks
	eloInput := make([]struct {
		UserID int
		Rating int
		Rank   int
	}, len(parts))
	for i, p := range parts {
		rank := i + 1
		if i > 0 && parts[i].Score == parts[i-1].Score && parts[i].Penalty == parts[i-1].Penalty {
			rank = eloInput[i-1].Rank
		}
		eloInput[i] = struct {
			UserID int
			Rating int
			Rank   int
		}{p.UserID, p.Rating, rank}
	}

	newRatings := calculateELO(eloInput)

	// Update in a transaction
	tx, err := h.DB.Begin(context.Background())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback(context.Background())

	for i, p := range parts {
		nr := newRatings[p.UserID]
		change := nr - p.Rating

		// Update contest_participants
		tx.Exec(context.Background(),
			`UPDATE app.contest_participants
 SET rank = $1, rating_before = $2, rating_after = $3, rating_change = $4
 WHERE contest_id = $5 AND user_id = $6`,
			eloInput[i].Rank, p.Rating, nr, change, contestID, p.UserID,
		)

		// Update user rating
		tx.Exec(context.Background(),
			`UPDATE app.users SET rating = $1 WHERE id = $2`, nr, p.UserID,
		)
	}

	if err := tx.Commit(context.Background()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	// Build response
	results := make([]gin.H, len(parts))
	for i, p := range parts {
		nr := newRatings[p.UserID]
		results[i] = gin.H{
			"user_id":       p.UserID,
			"rank":          eloInput[i].Rank,
			"rating_before": p.Rating,
			"rating_after":  nr,
			"rating_change": nr - p.Rating,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "Contest finalized",
		"contest_id": contestID,
		"results":    results,
	})
}

// ──────────────────────────────────────────────────────────────
// SSE endpoint: real-time rating predictions
// ──────────────────────────────────────────────────────────────

func (h *Handler) ContestRatingStream(c *gin.Context) {
	idStr := c.Param("id")
	contestID, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	// Set SSE headers
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
	c.Writer.Flush()

	ch := predictorHub.subscribe(contestID)
	defer predictorHub.unsubscribe(contestID, ch)

	// Send initial predictions
	initPredictions := h.predictRatings(contestID)
	if initPredictions != nil {
		data, _ := json.Marshal(gin.H{"type": "rating_update", "predictions": initPredictions})
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		c.Writer.Flush()
	}

	clientGone := c.Request.Context().Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-clientGone:
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(c.Writer, "data: %s\n\n", msg)
			c.Writer.Flush()
		case <-ticker.C:
			// Heartbeat
			fmt.Fprintf(c.Writer, ": heartbeat\n\n")
			c.Writer.Flush()
		}
	}
}

// ──────────────────────────────────────────────────────────────
// Rating predictions (non-streaming, one-shot)
// ──────────────────────────────────────────────────────────────

func (h *Handler) ContestRatingPredict(c *gin.Context) {
	idStr := c.Param("id")
	contestID, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid contest ID"})
		return
	}

	predictions := h.predictRatings(contestID)
	if predictions == nil {
		predictions = []RatingPrediction{}
	}

	c.JSON(http.StatusOK, gin.H{"predictions": predictions})
}

// ──────────────────────────────────────────────────────────────
// User's contest history
// ──────────────────────────────────────────────────────────────

func (h *Handler) UserContestHistory(c *gin.Context) {
	userID, _ := c.Get("userID")

	rows, err := h.DB.Query(context.Background(),
		`SELECT c.id, c.title, c.start_time, c.end_time, c.is_rated,
		        cp.score, cp.rank, cp.rating_before, cp.rating_after, cp.rating_change
		 FROM app.contest_participants cp
		 JOIN app.contests c ON c.id = cp.contest_id
		 WHERE cp.user_id = $1
		 ORDER BY c.start_time DESC`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	type entry struct {
		ContestID    int       `json:"contest_id"`
		Title        string    `json:"title"`
		StartTime    time.Time `json:"start_time"`
		EndTime      time.Time `json:"end_time"`
		IsRated      bool      `json:"is_rated"`
		Score        int       `json:"score"`
		Rank         *int      `json:"rank"`
		RatingBefore *int      `json:"rating_before"`
		RatingAfter  *int      `json:"rating_after"`
		RatingChange *int      `json:"rating_change"`
	}
	history := make([]entry, 0)
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.ContestID, &e.Title, &e.StartTime, &e.EndTime, &e.IsRated,
			&e.Score, &e.Rank, &e.RatingBefore, &e.RatingAfter, &e.RatingChange); err == nil {
			history = append(history, e)
		}
	}

	c.JSON(http.StatusOK, gin.H{"history": history})
}

// ──────────────────────────────────────────────────────────────
// Remove old stubs
// ──────────────────────────────────────────────────────────────

func (h *Handler) RegisterContest(c *gin.Context) {
	// No registration needed - users auto-join on first submission
	c.JSON(http.StatusOK, gin.H{"message": "No registration needed. Just submit a solution to join!"})
}

// ──────────────────────────────────────────────────────────────
// Global ratings page
// ──────────────────────────────────────────────────────────────

func (h *Handler) GlobalRatings(c *gin.Context) {
	rows, err := h.DB.Query(context.Background(),
		`SELECT id, username, rating FROM app.users ORDER BY rating DESC LIMIT 100`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	type userRating struct {
		UserID   int    `json:"user_id"`
		Username string `json:"username"`
		Rating   int    `json:"rating"`
		Rank     int    `json:"rank"`
	}

	ratings := make([]userRating, 0)
	for rows.Next() {
		var u userRating
		if err := rows.Scan(&u.UserID, &u.Username, &u.Rating); err == nil {
			u.Rank = len(ratings) + 1
			ratings = append(ratings, u)
		}
	}

	c.JSON(http.StatusOK, gin.H{"ratings": ratings})
}
