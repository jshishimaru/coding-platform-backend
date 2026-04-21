package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type ProblemCodeDraftRequest struct {
	Code     string `json:"code"`
	Language string `json:"language"`
}

type ProblemCodeDraftResponse struct {
	ProblemID int        `json:"problem_id"`
	Code      string     `json:"code"`
	Language  string     `json:"language"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	Exists    bool       `json:"exists"`
}

func normalizeDraftLanguage(language string) string {
	if language == "" {
		return "cpp"
	}
	return language
}

func (h *Handler) saveProblemCodeDraft(ctx context.Context, userID, problemID int, language, code string) error {
	language = normalizeDraftLanguage(language)
	if language != "cpp" {
		return errors.New("only C++ is supported currently")
	}

	_, err := h.DB.Exec(ctx,
		`INSERT INTO app.problem_code_drafts (user_id, problem_id, language, source_code, updated_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (user_id, problem_id) DO UPDATE
		 SET language = EXCLUDED.language,
		     source_code = EXCLUDED.source_code,
		     updated_at = NOW()`,
		userID, problemID, language, code,
	)
	return err
}

func parseProblemIDParam(c *gin.Context) (int, bool) {
	problemID, err := strconv.Atoi(c.Param("problemId"))
	if err != nil || problemID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
		return 0, false
	}
	return problemID, true
}

func (h *Handler) GetProblemCodeDraft(c *gin.Context) {
	problemID, ok := parseProblemIDParam(c)
	if !ok {
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)

	var res ProblemCodeDraftResponse
	res.ProblemID = problemID
	res.Language = "cpp"

	err := h.DB.QueryRow(context.Background(),
		`SELECT source_code, language, updated_at
		 FROM app.problem_code_drafts
		 WHERE user_id = $1 AND problem_id = $2`,
		uid, problemID,
	).Scan(&res.Code, &res.Language, &res.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			res.Exists = false
			c.JSON(http.StatusOK, res)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load saved code"})
		return
	}

	res.Exists = true
	c.JSON(http.StatusOK, res)
}

func (h *Handler) UpsertProblemCodeDraft(c *gin.Context) {
	problemID, ok := parseProblemIDParam(c)
	if !ok {
		return
	}

	var req ProblemCodeDraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	req.Language = normalizeDraftLanguage(req.Language)
	if req.Language != "cpp" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only C++ is supported currently"})
		return
	}

	userID, _ := c.Get("userID")
	uid := userID.(int)

	var exists bool
	if err := h.DB.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM app.problems WHERE id = $1)`,
		problemID,
	).Scan(&exists); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate problem"})
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Problem not found"})
		return
	}

	if err := h.saveProblemCodeDraft(context.Background(), uid, problemID, req.Language, req.Code); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save code"})
		return
	}

	var updatedAt time.Time
	if err := h.DB.QueryRow(context.Background(),
		`SELECT updated_at
		 FROM app.problem_code_drafts
		 WHERE user_id = $1 AND problem_id = $2`,
		uid, problemID,
	).Scan(&updatedAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load saved code"})
		return
	}

	c.JSON(http.StatusOK, ProblemCodeDraftResponse{
		ProblemID: problemID,
		Code:      req.Code,
		Language:  req.Language,
		UpdatedAt: &updatedAt,
		Exists:    true,
	})
}
