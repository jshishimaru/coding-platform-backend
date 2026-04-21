package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// ──────────────────────────────────────────────────────────────────────
// Dashboard response types
// ──────────────────────────────────────────────────────────────────────

type dashboardCounters struct {
	ProblemsTotal     int `json:"problems_total"`
	ProblemsPublished int `json:"problems_published"`
	ProblemsDraft     int `json:"problems_draft"`
	ContestsTotal     int `json:"contests_total"`
	ContestsUpcoming  int `json:"contests_upcoming"`
	ContestsRunning   int `json:"contests_running"`
	UsersTotal        int `json:"users_total"`
	UsersNew7d        int `json:"users_new_7d"`
	Submissions24h    int `json:"submissions_24h"`
	SubmissionsPrev   int `json:"submissions_prev_24h"`
	PendingReview     int `json:"pending_review"`
	PendingJoin       int `json:"pending_join"`
	JudgeQueue        int `json:"judge_queue"`
}

type chartPoint struct {
	Date     string `json:"date"` // YYYY-MM-DD (UTC)
	Total    int    `json:"total"`
	Accepted int    `json:"accepted"`
}

type countBucket struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type activeContest struct {
	ID              int       `json:"id"`
	Title           string    `json:"title"`
	Status          string    `json:"status"`
	StartTime       time.Time `json:"start_time"`
	EndTime         time.Time `json:"end_time"`
	ParticipantNum  int       `json:"participant_count"`
	ProblemNum      int       `json:"problem_count"`
	Proctored       bool      `json:"proctored"`
}

type recentSubmission struct {
	ID          int       `json:"id"`
	Username    string    `json:"username"`
	ProblemSlug string    `json:"problem_slug"`
	Language    string    `json:"language"`
	Status      string    `json:"status"`
	SubmittedAt time.Time `json:"submitted_at"`
}

type topUser struct {
	UserID      int    `json:"user_id"`
	Username    string `json:"username"`
	Submissions int    `json:"submissions"`
	Accepted    int    `json:"accepted"`
}

// AdminDashboard returns a rich set of metrics, queues, and activity feeds
// used by the admin portal home page.
func (h *Handler) AdminDashboard(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var counters dashboardCounters

	// ─── Counters (single round-trip each; errors are non-fatal so
	// the dashboard still renders with partial data if one query fails). ───
	_ = h.DB.QueryRow(ctx, `SELECT COUNT(*) FROM app.problems`).Scan(&counters.ProblemsTotal)
	_ = h.DB.QueryRow(ctx, `SELECT COUNT(*) FROM app.problems WHERE published_at IS NOT NULL`).Scan(&counters.ProblemsPublished)
	counters.ProblemsDraft = counters.ProblemsTotal - counters.ProblemsPublished

	_ = h.DB.QueryRow(ctx, `SELECT COUNT(*) FROM app.contests`).Scan(&counters.ContestsTotal)
	_ = h.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM app.contests
		WHERE status IN ('upcoming','draft') AND start_time > NOW()
	`).Scan(&counters.ContestsUpcoming)
	_ = h.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM app.contests
		WHERE start_time <= NOW() AND end_time >= NOW()
	`).Scan(&counters.ContestsRunning)

	_ = h.DB.QueryRow(ctx, `SELECT COUNT(*) FROM app.users`).Scan(&counters.UsersTotal)
	_ = h.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM app.users WHERE created_at >= NOW() - INTERVAL '7 days'
	`).Scan(&counters.UsersNew7d)

	_ = h.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM app.submissions
		WHERE submitted_at >= NOW() - INTERVAL '24 hours'
	`).Scan(&counters.Submissions24h)
	_ = h.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM app.submissions
		WHERE submitted_at >= NOW() - INTERVAL '48 hours'
		  AND submitted_at <  NOW() - INTERVAL '24 hours'
	`).Scan(&counters.SubmissionsPrev)

	_ = h.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM app.submissions WHERE status = 'pending_review'
	`).Scan(&counters.PendingReview)
	_ = h.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM app.group_join_requests WHERE status = 'pending'
	`).Scan(&counters.PendingJoin)
	_ = h.DB.QueryRow(ctx, `
		SELECT COUNT(*) FROM app.submissions WHERE status IN ('pending','judging')
	`).Scan(&counters.JudgeQueue)

	// ─── 14-day submission timeseries (UTC buckets) ───
	// generate_series fills zero-count days so the chart never has gaps.
	chart := make([]chartPoint, 0, 14)
	rows, err := h.DB.Query(ctx, `
		WITH days AS (
			SELECT generate_series(
				date_trunc('day', NOW() - INTERVAL '13 days'),
				date_trunc('day', NOW()),
				INTERVAL '1 day'
			)::date AS d
		)
		SELECT
			to_char(days.d, 'YYYY-MM-DD') AS date,
			COALESCE(SUM(CASE WHEN s.id IS NOT NULL THEN 1 ELSE 0 END), 0) AS total,
			COALESCE(SUM(CASE WHEN s.status = 'accepted' THEN 1 ELSE 0 END), 0) AS accepted
		FROM days
		LEFT JOIN app.submissions s
		  ON date_trunc('day', s.submitted_at) = days.d
		GROUP BY days.d
		ORDER BY days.d ASC
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p chartPoint
			if err := rows.Scan(&p.Date, &p.Total, &p.Accepted); err == nil {
				chart = append(chart, p)
			}
		}
	}

	// ─── Status breakdown (last 7 days) ───
	statusBreakdown := make([]countBucket, 0, 10)
	if r, err := h.DB.Query(ctx, `
		SELECT status, COUNT(*) AS c
		FROM app.submissions
		WHERE submitted_at >= NOW() - INTERVAL '7 days'
		GROUP BY status
		ORDER BY c DESC
	`); err == nil {
		defer r.Close()
		for r.Next() {
			var b countBucket
			if err := r.Scan(&b.Label, &b.Count); err == nil {
				statusBreakdown = append(statusBreakdown, b)
			}
		}
	}

	// ─── Language breakdown (last 7 days, top 6) ───
	langBreakdown := make([]countBucket, 0, 6)
	if r, err := h.DB.Query(ctx, `
		SELECT language, COUNT(*) AS c
		FROM app.submissions
		WHERE submitted_at >= NOW() - INTERVAL '7 days'
		GROUP BY language
		ORDER BY c DESC
		LIMIT 6
	`); err == nil {
		defer r.Close()
		for r.Next() {
			var b countBucket
			if err := r.Scan(&b.Label, &b.Count); err == nil {
				langBreakdown = append(langBreakdown, b)
			}
		}
	}

	// ─── Active / upcoming contests (running first, then upcoming ≤30d) ───
	activeContests := make([]activeContest, 0, 8)
	if r, err := h.DB.Query(ctx, `
		SELECT
			c.id, c.title, c.status, c.start_time, c.end_time, c.proctored,
			(SELECT COUNT(*) FROM app.contest_participants cp WHERE cp.contest_id = c.id) AS participants,
			(SELECT COUNT(*) FROM app.contest_problems  cp WHERE cp.contest_id = c.id) AS problems
		FROM app.contests c
		WHERE c.end_time >= NOW()
		  AND c.start_time <= NOW() + INTERVAL '30 days'
		ORDER BY
			(c.start_time <= NOW() AND c.end_time >= NOW()) DESC,
			c.start_time ASC
		LIMIT 6
	`); err == nil {
		defer r.Close()
		for r.Next() {
			var a activeContest
			if err := r.Scan(
				&a.ID, &a.Title, &a.Status, &a.StartTime, &a.EndTime, &a.Proctored,
				&a.ParticipantNum, &a.ProblemNum,
			); err == nil {
				activeContests = append(activeContests, a)
			}
		}
	}

	// ─── Recent submissions (latest 10, any status) ───
	recent := make([]recentSubmission, 0, 10)
	if r, err := h.DB.Query(ctx, `
		SELECT s.id, u.username, COALESCE(p.slug, ''), s.language, s.status, s.submitted_at
		FROM app.submissions s
		JOIN app.users u    ON u.id = s.user_id
		LEFT JOIN app.problems p ON p.id = s.problem_id
		ORDER BY s.submitted_at DESC
		LIMIT 10
	`); err == nil {
		defer r.Close()
		for r.Next() {
			var rs recentSubmission
			if err := r.Scan(
				&rs.ID, &rs.Username, &rs.ProblemSlug, &rs.Language, &rs.Status, &rs.SubmittedAt,
			); err == nil {
				recent = append(recent, rs)
			}
		}
	}

	// ─── Top users by submission volume (last 7 days) ───
	top := make([]topUser, 0, 5)
	if r, err := h.DB.Query(ctx, `
		SELECT s.user_id, u.username,
			COUNT(*) AS subs,
			COALESCE(SUM(CASE WHEN s.status = 'accepted' THEN 1 ELSE 0 END), 0) AS acc
		FROM app.submissions s
		JOIN app.users u ON u.id = s.user_id
		WHERE s.submitted_at >= NOW() - INTERVAL '7 days'
		GROUP BY s.user_id, u.username
		ORDER BY subs DESC
		LIMIT 5
	`); err == nil {
		defer r.Close()
		for r.Next() {
			var t topUser
			if err := r.Scan(&t.UserID, &t.Username, &t.Submissions, &t.Accepted); err == nil {
				top = append(top, t)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		// Legacy keys — kept so any older client that still reads them doesn't break.
		"problems_count":     counters.ProblemsTotal,
		"contests_count":     counters.ContestsTotal,
		"users_count":        counters.UsersTotal,
		"recent_submissions": counters.Submissions24h,

		"counters":           counters,
		"submissions_chart":  chart,
		"status_breakdown":   statusBreakdown,
		"language_breakdown": langBreakdown,
		"active_contests":    activeContests,
		"recent":             recent,
		"top_users":          top,
		"generated_at":       time.Now().UTC(),
	})
}
