package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func (h *Handler) SubmissionsHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"module":    "submissions",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"connections": gin.H{
			"database": h.checkDB(),
			"redis":    h.checkRedis(),
		},
	})
}

func (h *Handler) CreateSubmission(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": "Submit code not yet implemented"})
}

func (h *Handler) GetSubmission(c *gin.Context) {
	id := c.Param("id")
	c.JSON(http.StatusNotImplemented, gin.H{"message": "Get submission " + id + " not yet implemented"})
}

func (h *Handler) GetUserSubmissions(c *gin.Context) {
	userId := c.Param("userId")
	c.JSON(http.StatusNotImplemented, gin.H{"message": "Get user " + userId + " submissions not yet implemented"})
}

func (h *Handler) GetQuestionSubmissions(c *gin.Context) {
	questionId := c.Param("questionId")
	c.JSON(http.StatusNotImplemented, gin.H{"message": "Get question " + questionId + " submissions not yet implemented"})
}
