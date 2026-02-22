package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func (h *Handler) ContestsHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"module":    "contests",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"connections": gin.H{
			"database": h.checkDB(),
			"redis":    h.checkRedis(),
		},
	})
}

func (h *Handler) ListContests(c *gin.Context) {
	now := time.Now().UTC()
	c.JSON(http.StatusOK, gin.H{
		"contests": []gin.H{
			{
				"id":           "1",
				"name":         "Weekly Contest #128",
				"status":       "live",
				"startsAt":     now.Format(time.RFC3339),
				"endsAt":       now.Add(2 * time.Hour).Format(time.RFC3339),
				"participants": 342,
			},
			{
				"id":           "2",
				"name":         "Weekly Contest #129",
				"status":       "upcoming",
				"startsAt":     now.Add(7 * 24 * time.Hour).Format(time.RFC3339),
				"endsAt":       now.Add(7*24*time.Hour + 2*time.Hour).Format(time.RFC3339),
				"participants": 0,
			},
		},
		"total": 2,
	})
}

func (h *Handler) GetContest(c *gin.Context) {
	id := c.Param("id")
	c.JSON(http.StatusNotImplemented, gin.H{"message": "Get contest " + id + " not yet implemented"})
}

func (h *Handler) RegisterContest(c *gin.Context) {
	id := c.Param("id")
	c.JSON(http.StatusNotImplemented, gin.H{"message": "Register for contest " + id + " not yet implemented"})
}

func (h *Handler) ContestLeaderboard(c *gin.Context) {
	id := c.Param("id")
	c.JSON(http.StatusNotImplemented, gin.H{"message": "Leaderboard for contest " + id + " not yet implemented"})
}
