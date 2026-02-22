package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/coding-platform/backend/internal/sandbox"
)

// RunCodeRequest is the JSON body for POST /api/sandbox/run.
type RunCodeRequest struct {
	Code        string `json:"code" binding:"required"`
	Input       string `json:"input"`
	Language    string `json:"language" binding:"required"`
	TimeLimit   int    `json:"time_limit"`   // seconds (max 10)
	MemoryLimit int    `json:"memory_limit"` // MB      (max 256)
}

// RunCode compiles and executes user-submitted code in a sandbox.
func (h *Handler) RunCode(c *gin.Context) {
	var req RunCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Only C++ is supported for now
	if req.Language != "cpp" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only C++ is supported currently"})
		return
	}

	// Apply user-requested limits (within allowed maximums)
	cfg := sandbox.DefaultConfig()
	if req.TimeLimit > 0 && req.TimeLimit <= 10 {
		cfg.MaxTimeSec = req.TimeLimit
	}
	if req.MemoryLimit > 0 && req.MemoryLimit <= 256 {
		cfg.MaxMemoryMB = req.MemoryLimit
	}

	result := sandbox.RunCpp(req.Code, req.Input, cfg)
	c.JSON(http.StatusOK, result)
}

// SandboxHealth reports the sandbox service status.
func (h *Handler) SandboxHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "sandbox",
	})
}
