package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func (h *Handler) QuestionsHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"module":    "questions",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"connections": gin.H{
			"database": h.checkDB(),
			"redis":    h.checkRedis(),
		},
	})
}

func (h *Handler) ListQuestions(c *gin.Context) {
	// Sample data for connectivity testing
	c.JSON(http.StatusOK, gin.H{
		"questions": []gin.H{
			{"id": "1", "title": "Two Sum", "difficulty": "easy", "rating": 800, "tags": []string{"Array", "Hash Table"}},
			{"id": "2", "title": "Linked List Cycle II", "difficulty": "medium", "rating": 1400, "tags": []string{"Linked List", "Two Pointers"}},
			{"id": "3", "title": "Minimum Window Substring", "difficulty": "hard", "rating": 2000, "tags": []string{"String", "Sliding Window"}},
		},
		"total": 3,
		"page":  1,
		"limit": 20,
	})
}

func (h *Handler) GetQuestion(c *gin.Context) {
	id := c.Param("id")
	c.JSON(http.StatusNotImplemented, gin.H{"message": "Get question " + id + " not yet implemented"})
}

func (h *Handler) CreateQuestion(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": "Create question not yet implemented"})
}

func (h *Handler) ListTags(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"tags": []string{
			"Array", "String", "Hash Table", "Dynamic Programming", "Math",
			"Sorting", "Greedy", "Binary Search", "Tree", "Graph",
			"Linked List", "Two Pointers", "Sliding Window", "Stack", "Queue",
		},
	})
}
