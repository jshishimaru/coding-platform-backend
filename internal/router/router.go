package router

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/coding-platform/backend/internal/config"
	"github.com/coding-platform/backend/internal/handlers"
	"github.com/coding-platform/backend/internal/middleware"
)

func Setup(db *pgxpool.Pool, rdb *redis.Client, cfg *config.Config) *gin.Engine {
	r := gin.Default()

	// CORS middleware
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		AllowCredentials: true,
	}))

	// Create handler with dependencies
	h := handlers.New(db, rdb, cfg)

	// Auth middleware shorthand
	authRequired := middleware.AuthRequired(cfg, rdb)

	// API routes
	api := r.Group("/api")
	{
		// Health
		api.GET("/health", h.Health)

		// Auth routes (public)
		auth := api.Group("/auth")
		{
			auth.POST("/register", h.Register)
			auth.POST("/login", h.Login)
			auth.POST("/logout", authRequired, h.Logout)
			auth.GET("/me", authRequired, h.GetCurrentUser)
			auth.GET("/health", h.AuthHealth)
		}

		// Question routes
		questions := api.Group("/questions")
		{
			questions.GET("", h.ListQuestions)
			questions.GET("/tags/list", h.ListTags)
			questions.GET("/health", h.QuestionsHealth)
			questions.GET("/:slug", h.GetQuestion)
			questions.POST("", authRequired, h.CreateQuestion)
			questions.POST("/:slug/run", authRequired, h.RunSampleTests)
		}

		// Contest routes
		contests := api.Group("/contests")
		{
			contests.GET("", h.ListContests)
			contests.GET("/health", h.ContestsHealth)
			contests.GET("/ratings", h.GlobalRatings)
			contests.GET("/:id", h.GetContest)
			contests.GET("/:id/leaderboard", h.ContestLeaderboard)
			contests.GET("/:id/ratings/predict", h.ContestRatingPredict)
			contests.GET("/:id/ratings/stream", h.ContestRatingStream)
			contests.POST("", authRequired, h.CreateContest)
			contests.POST("/:id/submit", authRequired, h.ContestSubmit)
			contests.POST("/:id/finalize", authRequired, h.FinalizeContest)
			contests.GET("/history", authRequired, h.UserContestHistory)
		}

		// Submission routes (all protected)
		submissions := api.Group("/submissions")
		submissions.Use(authRequired)
		{
			submissions.POST("", h.CreateSubmission)
			submissions.GET("/health", h.SubmissionsHealth)
			submissions.GET("/question/:slug", h.GetQuestionSubmissions)
			submissions.GET("/:id", h.GetSubmission)
		}

		// Sandbox routes (protected)
		sandbox := api.Group("/sandbox")
		sandbox.Use(authRequired)
		{
			sandbox.POST("/run", h.RunCode)
			sandbox.GET("/health", h.SandboxHealth)
		}
	}

	return r
}
