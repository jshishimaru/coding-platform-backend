package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/coding-platform/backend/internal/middleware"
	"github.com/coding-platform/backend/internal/models"
)

// ---------- Request / response structs ----------

type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50"`
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

type LoginRequest struct {
	Login    string `json:"login"    binding:"required"` // username or email
	Password string `json:"password" binding:"required"`
}

type AuthResponse struct {
	Token          string      `json:"token"`
	User           models.User `json:"user"`
	CanAccessAdmin bool        `json:"can_access_admin"`
}

// ---------- Handlers ----------

func (h *Handler) AuthHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"module":    "auth",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"connections": gin.H{
			"database": h.checkDB(),
			"redis":    h.checkRedis(),
		},
	})
}

func (h *Handler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Insert user
	var user models.User
	err = h.DB.QueryRow(
		context.Background(),
		`INSERT INTO app.users (username, email, password_hash, role, rating)
		 VALUES ($1, $2, $3, 'user', 1200)
		 RETURNING id, username, email, role, rating, created_at`,
		req.Username, req.Email, string(hash),
	).Scan(&user.ID, &user.Username, &user.Email, &user.Role, &user.Rating, &user.CreatedAt)
	if err != nil {
		// Check for unique-violation
		errMsg := err.Error()
		if contains(errMsg, "users_username_key") {
			c.JSON(http.StatusConflict, gin.H{"error": "Username already taken"})
			return
		}
		if contains(errMsg, "users_email_key") {
			c.JSON(http.StatusConflict, gin.H{"error": "Email already registered"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	// Generate JWT
	token, err := middleware.GenerateToken(user.ID, user.Username, user.Role, h.Cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	// Cache user in Redis
	h.cacheUser(user)

	canAccessAdmin, _ := h.userCanAccessAdmin(context.Background(), user.ID, user.Role)
	c.JSON(http.StatusCreated, AuthResponse{Token: token, User: user, CanAccessAdmin: canAccessAdmin})
}

func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Look up user by username OR email
	var user models.User
	var passwordHash string
	err := h.DB.QueryRow(
		context.Background(),
		`SELECT id, username, email, password_hash, role, rating, created_at
		 FROM app.users
		 WHERE username = $1 OR email = $1`,
		req.Login,
	).Scan(&user.ID, &user.Username, &user.Email, &passwordHash, &user.Role, &user.Rating, &user.CreatedAt)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	// Generate JWT
	token, err := middleware.GenerateToken(user.ID, user.Username, user.Role, h.Cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	// Cache user in Redis
	h.cacheUser(user)

	canAccessAdmin, _ := h.userCanAccessAdmin(context.Background(), user.ID, user.Role)
	c.JSON(http.StatusOK, AuthResponse{Token: token, User: user, CanAccessAdmin: canAccessAdmin})
}

func (h *Handler) Logout(c *gin.Context) {
	tokenString, exists := c.Get("token")
	if !exists {
		c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
		return
	}

	// Blacklist the token in Redis until it naturally expires
	if h.Redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		expiry := time.Duration(h.Cfg.JWTExpiryHours) * time.Hour
		h.Redis.Set(ctx, "blacklist:"+tokenString.(string), "1", expiry)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Logged out successfully"})
}

func (h *Handler) GetCurrentUser(c *gin.Context) {
	userID, _ := c.Get("userID")

	// Try Redis cache first
	if h.Redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cached, err := h.Redis.HGetAll(ctx, fmt.Sprintf("user:%d", userID)).Result()
		if err == nil && len(cached) > 0 {
			uid, _ := userID.(int)
			canAccessAdmin, _ := h.userCanAccessAdmin(context.Background(), uid, cached["role"])
			c.JSON(http.StatusOK, gin.H{
				"id":               userID,
				"username":         cached["username"],
				"email":            cached["email"],
				"role":             cached["role"],
				"rating":           cached["rating"],
				"created_at":       cached["created_at"],
				"can_access_admin": canAccessAdmin,
			})
			return
		}
	}

	// Fallback to DB
	var user models.User
	err := h.DB.QueryRow(
		context.Background(),
		`SELECT id, username, email, role, rating, created_at
		 FROM app.users WHERE id = $1`, userID,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Role, &user.Rating, &user.CreatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	h.cacheUser(user)
	canAccessAdmin, _ := h.userCanAccessAdmin(context.Background(), user.ID, user.Role)
	c.JSON(http.StatusOK, gin.H{
		"id":               user.ID,
		"username":         user.Username,
		"email":            user.Email,
		"role":             user.Role,
		"rating":           user.Rating,
		"created_at":       user.CreatedAt,
		"can_access_admin": canAccessAdmin,
	})
}

// ---------- Helpers ----------

func (h *Handler) cacheUser(user models.User) {
	if h.Redis == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	key := fmt.Sprintf("user:%d", user.ID)
	h.Redis.HSet(ctx, key, map[string]interface{}{
		"username":   user.Username,
		"email":      user.Email,
		"role":       user.Role,
		"rating":     user.Rating,
		"created_at": user.CreatedAt.Format(time.RFC3339),
	})
	h.Redis.Expire(ctx, key, 30*time.Minute)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
