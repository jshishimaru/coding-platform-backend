package handlers

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// AdminExportContestCSV streams contest results as a CSV file.
// Columns: rank, username, email, score, penalty_minutes,
//
//	rating_before, rating_after, rating_change,
//	<per-problem columns: P<order>_score>, submitted_count
//
// Works for both global and group contests.
func (h *Handler) AdminExportContestCSV(c *gin.Context) {
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
		c.JSON(http.StatusForbidden, gin.H{"error": "You can only export contests for managed group contests"})
		return
	}

	ctx := context.Background()

	// Contest title (for filename)
	var contestTitle string
	_ = h.DB.QueryRow(ctx,
		`SELECT title FROM app.contests WHERE id = $1`, contestID,
	).Scan(&contestTitle)
	if contestTitle == "" {
		contestTitle = fmt.Sprintf("contest_%d", contestID)
	}

	// Problem columns
	probRows, err := h.DB.Query(ctx,
		`SELECT cp.problem_id, cp.problem_order, p.slug, cp.max_points
		 FROM app.contest_problems cp
		 JOIN app.problems p ON p.id = cp.problem_id
		 WHERE cp.contest_id = $1
		 ORDER BY cp.problem_order`, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer probRows.Close()

	type probCol struct {
		ID    int
		Order int
		Slug  string
		Max   int
	}
	var problems []probCol
	for probRows.Next() {
		var p probCol
		if err := probRows.Scan(&p.ID, &p.Order, &p.Slug, &p.Max); err == nil {
			problems = append(problems, p)
		}
	}

	// Participants
	partRows, err := h.DB.Query(ctx,
		`SELECT cp.user_id, u.username, u.email, cp.score, cp.penalty_time,
		        cp.rank, cp.rating_before, cp.rating_after, cp.rating_change
		 FROM app.contest_participants cp
		 JOIN app.users u ON u.id = cp.user_id
		 WHERE cp.contest_id = $1
		 ORDER BY cp.score DESC, cp.penalty_time ASC`, contestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer partRows.Close()

	type partRow struct {
		UserID       int
		Username     string
		Email        string
		Score        int
		Penalty      int
		Rank         *int
		RatingBefore *int
		RatingAfter  *int
		RatingChange *int
	}
	var participants []partRow
	for partRows.Next() {
		var p partRow
		if err := partRows.Scan(&p.UserID, &p.Username, &p.Email, &p.Score, &p.Penalty,
			&p.Rank, &p.RatingBefore, &p.RatingAfter, &p.RatingChange); err == nil {
			participants = append(participants, p)
		}
	}

	// Per-user per-problem scores
	scoreRows, err := h.DB.Query(ctx,
		`SELECT user_id, problem_id, points_earned
		 FROM app.contest_solves WHERE contest_id = $1`, contestID)
	scores := make(map[int]map[int]int)
	if err == nil {
		defer scoreRows.Close()
		for scoreRows.Next() {
			var uid, pid, pts int
			if err := scoreRows.Scan(&uid, &pid, &pts); err == nil {
				if scores[uid] == nil {
					scores[uid] = make(map[int]int)
				}
				scores[uid][pid] = pts
			}
		}
	}

	// Submission counts per user (contest-scoped)
	subCounts := make(map[int]int)
	subRows, err := h.DB.Query(ctx,
		`SELECT user_id, COUNT(*) FROM app.submissions
		 WHERE contest_id = $1 GROUP BY user_id`, contestID)
	if err == nil {
		defer subRows.Close()
		for subRows.Next() {
			var uid, n int
			if err := subRows.Scan(&uid, &n); err == nil {
				subCounts[uid] = n
			}
		}
	}

	// Stream CSV
	filename := fmt.Sprintf("contest_%d_%s_%s.csv",
		contestID, sanitize(contestTitle), time.Now().UTC().Format("20060102_150405"))
	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	w := csv.NewWriter(c.Writer)

	// Header row
	header := []string{
		"rank", "username", "email", "total_score", "penalty_minutes",
		"rating_before", "rating_after", "rating_change",
	}
	for _, p := range problems {
		header = append(header, fmt.Sprintf("P%d_%s", p.Order, p.Slug))
	}
	header = append(header, "submission_count")
	_ = w.Write(header)

	// Body rows. Re-compute rank from sorted order (in case rank column is null).
	prevScore, prevPenalty, rank := -1, -1, 0
	for i, p := range participants {
		if i == 0 || p.Score != prevScore || p.Penalty != prevPenalty {
			rank = i + 1
		}
		prevScore, prevPenalty = p.Score, p.Penalty

		row := []string{
			strconv.Itoa(rank),
			p.Username,
			p.Email,
			strconv.Itoa(p.Score),
			strconv.Itoa(p.Penalty),
			intPtrString(p.RatingBefore),
			intPtrString(p.RatingAfter),
			intPtrString(p.RatingChange),
		}
		for _, prob := range problems {
			if m, ok := scores[p.UserID]; ok {
				if s, ok := m[prob.ID]; ok {
					row = append(row, strconv.Itoa(s))
					continue
				}
			}
			row = append(row, "0")
		}
		row = append(row, strconv.Itoa(subCounts[p.UserID]))
		_ = w.Write(row)
	}

	w.Flush()

	// Audit log
	h.logAudit(uid, "contest.export", "contest", contestID, map[string]interface{}{
		"participants": len(participants),
	}, c.ClientIP())
}

// sanitize returns a filename-safe lowercased version of s.
func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch {
		case b >= 'A' && b <= 'Z':
			out = append(out, b+32)
		case b >= 'a' && b <= 'z', b >= '0' && b <= '9', b == '-', b == '_':
			out = append(out, b)
		case b == ' ':
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "contest"
	}
	return string(out)
}

func intPtrString(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}
