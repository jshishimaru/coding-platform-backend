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
			questions.PUT("/:slug/tags", authRequired, h.UpdateQuestionTags)
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
			submissions.GET("/me", h.GetUserSubmissions)
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

		// ── Admin routes ──────────────────────────────────────────────
		admin := api.Group("/admin")
		admin.Use(authRequired)
		{
			// ── Problem management ────────────────────────────────────
			adminProblems := admin.Group("/problems")
			adminProblems.Use(middleware.RequireSetterOrAbove())
			{
				adminProblems.GET("", h.AdminListProblems)
				adminProblems.POST("", h.AdminCreateProblem)
			}

			// Problem-specific routes (access-controlled per problem)
			adminProblem := admin.Group("/problems/:id")
			{
				adminProblem.GET("", middleware.RequireProblemAccess(db, "viewer"), h.AdminGetProblem)
				adminProblem.PUT("", middleware.RequireProblemAccess(db, "editor"), h.AdminUpdateProblem)
				adminProblem.DELETE("", middleware.RequireAdmin(), h.AdminDeleteProblem)

				// Revisions
				adminProblem.GET("/revisions", middleware.RequireProblemAccess(db, "viewer"), h.AdminListRevisions)
				adminProblem.POST("/revisions/:rev/activate", middleware.RequireProblemAccess(db, "editor"), h.AdminActivateRevision)

				// Publish
				adminProblem.POST("/publish", middleware.RequireProblemAccess(db, "editor"), h.AdminPublishProblem)

				// Access control
				adminProblem.GET("/access", middleware.RequireProblemAccess(db, "owner"), h.AdminListProblemAccess)
				adminProblem.POST("/access", middleware.RequireProblemAccess(db, "owner"), h.AdminGrantProblemAccess)
				adminProblem.DELETE("/access/:userId", middleware.RequireProblemAccess(db, "owner"), h.AdminRevokeProblemAccess)

				// Test cases
				adminProblem.GET("/tests", middleware.RequireProblemAccess(db, "viewer"), h.AdminListTests)
				adminProblem.POST("/tests", middleware.RequireProblemAccess(db, "editor"), h.AdminCreateTest)
				adminProblem.POST("/tests/bulk", middleware.RequireProblemAccess(db, "editor"), h.AdminBulkCreateTests)
				adminProblem.PUT("/tests/:testId", middleware.RequireProblemAccess(db, "editor"), h.AdminUpdateTest)
				adminProblem.DELETE("/tests/:testId", middleware.RequireProblemAccess(db, "editor"), h.AdminDeleteTest)
				adminProblem.POST("/tests/generate", middleware.RequireProblemAccess(db, "editor"), h.AdminGenerateTests)
				adminProblem.POST("/tests/validate", middleware.RequireProblemAccess(db, "editor"), h.AdminValidateTests)
				adminProblem.POST("/tests/reorder", middleware.RequireProblemAccess(db, "editor"), h.AdminReorderTests)

				// Generators
				adminProblem.GET("/generators", middleware.RequireProblemAccess(db, "viewer"), h.AdminListGenerators)
				adminProblem.POST("/generators", middleware.RequireProblemAccess(db, "editor"), h.AdminCreateGenerator)
				adminProblem.PUT("/generators/:compId", middleware.RequireProblemAccess(db, "editor"), h.AdminUpdateGenerator)
				adminProblem.DELETE("/generators/:compId", middleware.RequireProblemAccess(db, "editor"), h.AdminDeleteGenerator)
				adminProblem.POST("/generators/:compId/compile", middleware.RequireProblemAccess(db, "editor"), h.AdminCompileGenerator)

				// Validators
				adminProblem.GET("/validators", middleware.RequireProblemAccess(db, "viewer"), h.AdminListValidators)
				adminProblem.POST("/validators", middleware.RequireProblemAccess(db, "editor"), h.AdminCreateValidator)
				adminProblem.PUT("/validators/:compId", middleware.RequireProblemAccess(db, "editor"), h.AdminUpdateValidator)
				adminProblem.DELETE("/validators/:compId", middleware.RequireProblemAccess(db, "editor"), h.AdminDeleteValidator)
				adminProblem.POST("/validators/:compId/compile", middleware.RequireProblemAccess(db, "editor"), h.AdminCompileValidator)
				adminProblem.POST("/validators/:compId/activate", middleware.RequireProblemAccess(db, "editor"), h.AdminActivateValidator)

				// Checkers
				adminProblem.GET("/checkers", middleware.RequireProblemAccess(db, "viewer"), h.AdminListCheckers)
				adminProblem.POST("/checkers", middleware.RequireProblemAccess(db, "editor"), h.AdminCreateChecker)
				adminProblem.PUT("/checkers/:compId", middleware.RequireProblemAccess(db, "editor"), h.AdminUpdateChecker)
				adminProblem.DELETE("/checkers/:compId", middleware.RequireProblemAccess(db, "editor"), h.AdminDeleteChecker)
				adminProblem.POST("/checkers/:compId/compile", middleware.RequireProblemAccess(db, "editor"), h.AdminCompileChecker)
				adminProblem.POST("/checkers/:compId/activate", middleware.RequireProblemAccess(db, "editor"), h.AdminActivateChecker)

				// Interactors
				adminProblem.GET("/interactors", middleware.RequireProblemAccess(db, "viewer"), h.AdminListInteractors)
				adminProblem.POST("/interactors", middleware.RequireProblemAccess(db, "editor"), h.AdminCreateInteractor)
				adminProblem.PUT("/interactors/:compId", middleware.RequireProblemAccess(db, "editor"), h.AdminUpdateInteractor)
				adminProblem.DELETE("/interactors/:compId", middleware.RequireProblemAccess(db, "editor"), h.AdminDeleteInteractor)
				adminProblem.POST("/interactors/:compId/compile", middleware.RequireProblemAccess(db, "editor"), h.AdminCompileInteractor)
				adminProblem.POST("/interactors/:compId/activate", middleware.RequireProblemAccess(db, "editor"), h.AdminActivateInteractor)

				// Solutions
				adminProblem.GET("/solutions", middleware.RequireProblemAccess(db, "viewer"), h.AdminListSolutions)
				adminProblem.POST("/solutions", middleware.RequireProblemAccess(db, "editor"), h.AdminCreateSolution)
				adminProblem.PUT("/solutions/:compId", middleware.RequireProblemAccess(db, "editor"), h.AdminUpdateSolution)
				adminProblem.DELETE("/solutions/:compId", middleware.RequireProblemAccess(db, "editor"), h.AdminDeleteSolution)
				adminProblem.POST("/solutions/:compId/compile", middleware.RequireProblemAccess(db, "editor"), h.AdminCompileSolution)

				// Testing
				adminProblem.POST("/test-solutions", middleware.RequireProblemAccess(db, "tester"), h.AdminTestSolution)
				adminProblem.POST("/stress-test", middleware.RequireProblemAccess(db, "editor"), h.AdminStressTest)
			}

			// ── Contest management ────────────────────────────────────
			adminContests := admin.Group("/contests")
			adminContests.Use(middleware.RequireSetterOrAbove())
			{
				adminContests.GET("", h.AdminListContests)
				adminContests.POST("", h.AdminCreateContest)
				adminContests.GET("/:id", h.AdminGetContest)
				adminContests.PUT("/:id", h.AdminUpdateContest)
				adminContests.DELETE("/:id", middleware.RequireAdmin(), h.AdminDeleteContest)
				adminContests.POST("/:id/problems", h.AdminAddContestProblem)
				adminContests.PUT("/:id/problems/:cpId", h.AdminUpdateContestProblem)
				adminContests.DELETE("/:id/problems/:cpId", h.AdminRemoveContestProblem)
				adminContests.POST("/:id/publish", h.AdminPublishContest)
				adminContests.POST("/:id/finalize", middleware.RequireAdmin(), h.AdminFinalizeContestAdmin)
			}

			// ── User management (admin only) ──────────────────────────
			adminUsers := admin.Group("/users")
			adminUsers.Use(middleware.RequireAdmin())
			{
				adminUsers.GET("", h.AdminListUsers)
				adminUsers.GET("/:id", h.AdminGetUser)
				adminUsers.PUT("/:id/role", h.AdminUpdateUserRole)
			}

			// ── Audit log (admin only) ────────────────────────────────
			admin.GET("/audit-log", middleware.RequireAdmin(), h.AdminGetAuditLog)
		}
	}

	return r
}
