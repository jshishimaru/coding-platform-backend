package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/coding-platform/backend/internal/sandbox"
)

// ---------- Request / response types ----------

type CreateQuestionRequest struct {
	Title         string           `json:"title" binding:"required"`
	Slug          string           `json:"slug" binding:"required"`
	Statement     string           `json:"statement" binding:"required"`
	Difficulty    string           `json:"difficulty" binding:"required"`
	TimeLimitMs   int              `json:"time_limit_ms"`
	MemoryLimitMb int              `json:"memory_limit_mb"`
	CheckerCode   string           `json:"checker_code"`
	TestCases     []CreateTestCase `json:"test_cases" binding:"required"`
	Tags          []string         `json:"tags"`
}

type CreateTestCase struct {
	Input          string `json:"input"`
	ExpectedOutput string `json:"expected_output"`
	IsSample       bool   `json:"is_sample"`
}

type QuestionSummary struct {
	ID            int       `json:"id"`
	Title         string    `json:"title"`
	Slug          string    `json:"slug"`
	Difficulty    string    `json:"difficulty"`
	TimeLimitMs   int       `json:"time_limit_ms"`
	MemoryLimitMb int       `json:"memory_limit_mb"`
	CreatedAt     time.Time `json:"created_at"`
	Tags          []string  `json:"tags"`
}

type QuestionDetail struct {
	ID            int              `json:"id"`
	Title         string           `json:"title"`
	Slug          string           `json:"slug"`
	Statement     string           `json:"statement"`
	Difficulty    string           `json:"difficulty"`
	TimeLimitMs   int              `json:"time_limit_ms"`
	MemoryLimitMb int              `json:"memory_limit_mb"`
	CreatedAt     time.Time        `json:"created_at"`
	Tags          []string         `json:"tags"`
	SampleTests   []SampleTestCase `json:"sample_test_cases"`
}

type SampleTestCase struct {
	ID             int    `json:"id"`
	Input          string `json:"input"`
	ExpectedOutput string `json:"expected_output"`
}

type RunSampleRequest struct {
	Code     string `json:"code" binding:"required"`
	Language string `json:"language" binding:"required"`
}

// ---------- Handlers ----------

func (h *Handler) QuestionsHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok", "module": "questions",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handler) ListQuestions(c *gin.Context) {
	// Parse optional tag filter: ?tags=Array,Graph
	tagFilter := c.Query("tags")
	var filterTags []string
	if tagFilter != "" {
		for _, t := range strings.Split(tagFilter, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				filterTags = append(filterTags, t)
			}
		}
	}

	var rows interface {
		Next() bool
		Scan(dest ...any) error
		Close()
	}
	var err error

	if len(filterTags) > 0 {
		// Only return problems that have ALL the requested tags
		rows2, err2 := h.DB.Query(context.Background(),
			`SELECT p.id, p.title, p.slug, p.difficulty, p.time_limit_ms, p.memory_limit_mb, p.created_at
			 FROM app.problems p
			 WHERE p.id IN (
			   SELECT pt.problem_id FROM app.problem_tags pt
			   JOIN app.tags t ON t.id = pt.tag_id
			   WHERE t.name = ANY($1)
			   GROUP BY pt.problem_id
			   HAVING COUNT(DISTINCT t.name) = $2
			 )
			 ORDER BY p.id`, filterTags, len(filterTags))
		if err2 != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
			return
		}
		rows = rows2
		defer rows2.Close()
	} else {
		rows2, err2 := h.DB.Query(context.Background(),
			`SELECT id, title, slug, difficulty, time_limit_ms, memory_limit_mb, created_at
			 FROM app.problems ORDER BY id`)
		if err2 != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
			return
		}
		rows = rows2
		defer rows2.Close()
	}
	_ = err

	questions := make([]QuestionSummary, 0)
	for rows.Next() {
		var q QuestionSummary
		if err := rows.Scan(&q.ID, &q.Title, &q.Slug, &q.Difficulty,
			&q.TimeLimitMs, &q.MemoryLimitMb, &q.CreatedAt); err != nil {
			continue
		}
		q.Tags = make([]string, 0)
		questions = append(questions, q)
	}

	// Fetch tags for all problems in one query
	if len(questions) > 0 {
		ids := make([]int, len(questions))
		idxMap := make(map[int]int) // problem_id -> index in questions
		for i, q := range questions {
			ids[i] = q.ID
			idxMap[q.ID] = i
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
						questions[idx].Tags = append(questions[idx].Tags, name)
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"questions": questions,
		"total":     len(questions),
	})
}

func (h *Handler) GetQuestion(c *gin.Context) {
	slug := c.Param("slug")

	var q QuestionDetail
	err := h.DB.QueryRow(context.Background(),
		`SELECT id, title, slug, statement, difficulty, time_limit_ms, memory_limit_mb, created_at
		 FROM app.problems WHERE slug = $1`, slug,
	).Scan(&q.ID, &q.Title, &q.Slug, &q.Statement, &q.Difficulty, &q.TimeLimitMs, &q.MemoryLimitMb, &q.CreatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Question not found"})
		return
	}

	// Fetch sample test cases
	q.SampleTests = make([]SampleTestCase, 0)
	rows, err := h.DB.Query(context.Background(),
		`SELECT id, input, expected_output FROM app.test_cases
		 WHERE problem_id = $1 AND is_sample = true ORDER BY id`, q.ID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var tc SampleTestCase
			if err := rows.Scan(&tc.ID, &tc.Input, &tc.ExpectedOutput); err == nil {
				q.SampleTests = append(q.SampleTests, tc)
			}
		}
	}

	// Fetch tags
	q.Tags = make([]string, 0)
	tagRows, err := h.DB.Query(context.Background(),
		`SELECT t.name FROM app.problem_tags pt
		 JOIN app.tags t ON t.id = pt.tag_id
		 WHERE pt.problem_id = $1 ORDER BY t.name`, q.ID)
	if err == nil {
		defer tagRows.Close()
		for tagRows.Next() {
			var name string
			if err := tagRows.Scan(&name); err == nil {
				q.Tags = append(q.Tags, name)
			}
		}
	}

	c.JSON(http.StatusOK, q)
}

func (h *Handler) CreateQuestion(c *gin.Context) {
	var req CreateQuestionRequest
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
	req.Slug = strings.ToLower(strings.ReplaceAll(req.Slug, " ", "-"))

	userID, _ := c.Get("userID")

	tx, err := h.DB.Begin(context.Background())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback(context.Background())

	var problemID int
	err = tx.QueryRow(context.Background(),
		`INSERT INTO app.problems (title, slug, statement, difficulty, time_limit_ms, memory_limit_mb, checker_code, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		req.Title, req.Slug, req.Statement, req.Difficulty,
		req.TimeLimitMs, req.MemoryLimitMb, req.CheckerCode, userID,
	).Scan(&problemID)
	if err != nil {
		if strings.Contains(err.Error(), "problems_slug_key") {
			c.JSON(http.StatusConflict, gin.H{"error": "A question with this slug already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create question"})
		return
	}

	for _, tc := range req.TestCases {
		_, err := tx.Exec(context.Background(),
			`INSERT INTO app.test_cases (problem_id, input, expected_output, is_sample)
			 VALUES ($1, $2, $3, $4)`,
			problemID, tc.Input, tc.ExpectedOutput, tc.IsSample,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create test case"})
			return
		}
	}

	// Insert tags
	for _, tagName := range req.Tags {
		tagName = strings.TrimSpace(tagName)
		if tagName == "" {
			continue
		}
		_, err := tx.Exec(context.Background(),
			`INSERT INTO app.problem_tags (problem_id, tag_id)
			 SELECT $1, t.id FROM app.tags t WHERE t.name = $2
			 ON CONFLICT DO NOTHING`,
			problemID, tagName,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to assign tag"})
			return
		}
	}

	if err := tx.Commit(context.Background()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": problemID, "slug": req.Slug, "message": "Question created"})
}

func (h *Handler) RunSampleTests(c *gin.Context) {
	slug := c.Param("slug")

	var req RunSampleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Language != "cpp" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only C++ is supported currently"})
		return
	}

	// Fetch problem limits & checker
	var problemID, timeLimitMs, memoryLimitMb int
	var checkerCode string
	err := h.DB.QueryRow(context.Background(),
		`SELECT id, time_limit_ms, memory_limit_mb, checker_code
		 FROM app.problems WHERE slug = $1`, slug,
	).Scan(&problemID, &timeLimitMs, &memoryLimitMb, &checkerCode)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Question not found"})
		return
	}

	// Fetch sample test cases only
	rows, err := h.DB.Query(context.Background(),
		`SELECT id, input, expected_output, is_sample
		 FROM app.test_cases WHERE problem_id = $1 AND is_sample = true ORDER BY id`, problemID)
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "No sample test cases found"})
		return
	}

	result := sandbox.Judge(&sandbox.JudgeRequest{
		Code:          req.Code,
		Language:      req.Language,
		TestCases:     testCases,
		CheckerCode:   checkerCode,
		TimeLimitMs:   timeLimitMs,
		MemoryLimitMB: memoryLimitMb,
	})

	c.JSON(http.StatusOK, result)
}

// UpdateQuestionTags replaces the tags on an existing problem.
// PUT /questions/:slug/tags   body: {"tags": ["Array", "Two Pointers"]}
func (h *Handler) UpdateQuestionTags(c *gin.Context) {
	slug := c.Param("slug")

	var req struct {
		Tags []string `json:"tags" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Fetch problem ID
	var problemID int
	err := h.DB.QueryRow(context.Background(),
		`SELECT id FROM app.problems WHERE slug = $1`, slug,
	).Scan(&problemID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Question not found"})
		return
	}

	tx, err := h.DB.Begin(context.Background())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}
	defer tx.Rollback(context.Background())

	// Remove existing tags
	_, _ = tx.Exec(context.Background(),
		`DELETE FROM app.problem_tags WHERE problem_id = $1`, problemID)

	// Insert new tags
	for _, tagName := range req.Tags {
		tagName = strings.TrimSpace(tagName)
		if tagName == "" {
			continue
		}
		_, err := tx.Exec(context.Background(),
			`INSERT INTO app.problem_tags (problem_id, tag_id)
			 SELECT $1, t.id FROM app.tags t WHERE t.name = $2
			 ON CONFLICT DO NOTHING`,
			problemID, tagName,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to assign tag"})
			return
		}
	}

	if err := tx.Commit(context.Background()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Tags updated", "slug": slug, "tags": req.Tags})
}

func (h *Handler) ListTags(c *gin.Context) {
	rows, err := h.DB.Query(context.Background(), `SELECT name FROM app.tags ORDER BY name`)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"tags": []string{}})
		return
	}
	defer rows.Close()

	tags := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			tags = append(tags, name)
		}
	}
	c.JSON(http.StatusOK, gin.H{"tags": tags})
}
