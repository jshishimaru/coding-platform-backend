package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/coding-platform/backend/internal/config"
)

// Handler holds shared dependencies for all route handlers.
type Handler struct {
	DB    *pgxpool.Pool
	Redis *redis.Client
	Cfg   *config.Config
}

func New(db *pgxpool.Pool, rdb *redis.Client, cfg *config.Config) *Handler {
	return &Handler{DB: db, Redis: rdb, Cfg: cfg}
}

// checkDB pings PostgreSQL and returns status string.
func (h *Handler) checkDB() string {
	if h.DB == nil {
		return "not configured"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.DB.Ping(ctx); err != nil {
		return "error"
	}
	return "connected"
}

// checkRedis pings Redis and returns status string.
func (h *Handler) checkRedis() string {
	if h.Redis == nil {
		return "not configured"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.Redis.Ping(ctx).Err(); err != nil {
		return "error"
	}
	return "connected"
}

// Health is the global health endpoint.
func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"service":   "coding-platform-backend",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"connections": gin.H{
			"database": h.checkDB(),
			"redis":    h.checkRedis(),
		},
	})
}
