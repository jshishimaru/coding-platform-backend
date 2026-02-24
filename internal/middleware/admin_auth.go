package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Role hierarchy: admin > setter > tester > user
var roleLevel = map[string]int{
	"user":   0,
	"tester": 1,
	"setter": 2,
	"admin":  3,
}

// RequireRole returns middleware that enforces the user has one of the given roles.
func RequireRole(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			return
		}
		roleStr, ok := role.(string)
		if !ok || !allowed[roleStr] {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
			return
		}
		c.Next()
	}
}

// RequireAdmin is shorthand middleware for admin-only access.
func RequireAdmin() gin.HandlerFunc {
	return RequireRole("admin")
}

// RequireSetterOrAbove enforces setter or admin role.
func RequireSetterOrAbove() gin.HandlerFunc {
	return RequireRole("admin", "setter")
}

// RequireTesterOrAbove enforces tester, setter, or admin role.
func RequireTesterOrAbove() gin.HandlerFunc {
	return RequireRole("admin", "setter", "tester")
}

// RequireMinRole enforces a minimum role level using the role hierarchy.
func RequireMinRole(minRole string) gin.HandlerFunc {
	minLevel, ok := roleLevel[minRole]
	if !ok {
		minLevel = 99 // unknown role = deny all
	}
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			return
		}
		roleStr, _ := role.(string)
		userLevel, ok := roleLevel[roleStr]
		if !ok || userLevel < minLevel {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
			return
		}
		c.Next()
	}
}

// RequireProblemAccess checks per-problem access from the problem_access table.
// minAccessRole is one of: "viewer", "tester", "editor", "owner".
// Global admins bypass the check. Setters who created the problem have owner access.
func RequireProblemAccess(db *pgxpool.Pool, minAccessRole string) gin.HandlerFunc {
	accessLevel := map[string]int{
		"viewer": 0,
		"tester": 1,
		"editor": 2,
		"owner":  3,
	}

	minLevel, ok := accessLevel[minAccessRole]
	if !ok {
		minLevel = 99
	}

	return func(c *gin.Context) {
		// Global admins have full access
		role, _ := c.Get("role")
		if role == "admin" {
			c.Next()
			return
		}

		userID, _ := c.Get("userID")
		uid, ok := userID.(int)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			return
		}

		problemIDStr := c.Param("id")
		problemID, err := strconv.Atoi(problemIDStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid problem ID"})
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// Check if user is the problem creator (owner access)
		var createdBy int
		err = db.QueryRow(ctx, `SELECT created_by FROM app.problems WHERE id = $1`, problemID).Scan(&createdBy)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "Problem not found"})
			return
		}
		if createdBy == uid {
			// Creator has owner access
			c.Set("problemAccessRole", "owner")
			c.Next()
			return
		}

		// Check problem_access table
		var accessRole string
		err = db.QueryRow(ctx,
			`SELECT role FROM app.problem_access WHERE problem_id = $1 AND user_id = $2`,
			problemID, uid,
		).Scan(&accessRole)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": fmt.Sprintf("You need at least '%s' access to this problem", minAccessRole),
			})
			return
		}

		userAccessLevel, ok := accessLevel[accessRole]
		if !ok || userAccessLevel < minLevel {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": fmt.Sprintf("You need at least '%s' access to this problem (you have '%s')", minAccessRole, accessRole),
			})
			return
		}

		c.Set("problemAccessRole", accessRole)
		c.Next()
	}
}
