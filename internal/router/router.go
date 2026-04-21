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
	authOptional := middleware.AuthOptional(cfg, rdb)
	activeUser := middleware.RequireActiveUser(db)

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
			auth.POST("/logout", authRequired, activeUser, h.Logout)
			auth.GET("/me", authRequired, activeUser, h.GetCurrentUser)
			auth.GET("/health", h.AuthHealth)
		}

		// Question routes. List + detail are optional-auth so anonymous users
		// see only published problems, while an authenticated admin still
		// sees drafts for preview.
		questions := api.Group("/questions")
		{
			questions.GET("", authOptional, h.ListQuestions)
			questions.GET("/health", h.QuestionsHealth)
			questions.POST("", authRequired, activeUser, h.CreateQuestion)
		}

		// Tags routes (separate from /questions/:slug to avoid Gin routing conflicts)
		api.GET("/tags", h.ListTags)

		// Saved code drafts: one current draft per user/problem.
		codeDrafts := api.Group("/code-drafts")
		codeDrafts.Use(authRequired, activeUser)
		{
			codeDrafts.GET("/problems/:problemId", h.GetProblemCodeDraft)
			codeDrafts.PUT("/problems/:problemId", h.UpsertProblemCodeDraft)
		}

		// Question slug routes (no static sub-paths to avoid wildcard conflicts)
		questionBySlug := api.Group("/questions/:slug")
		{
			questionBySlug.GET("", authOptional, h.GetQuestion)
			questionBySlug.PUT("/tags", authRequired, activeUser, h.UpdateQuestionTags)
			questionBySlug.POST("/run", authRequired, activeUser, h.RunSampleTests)
		}

		// Contest routes
		contests := api.Group("/contests")
		{
			contests.GET("/health", h.ContestsHealth)
			contests.GET("/ratings", h.GlobalRatings)
			contests.GET("/history", authRequired, activeUser, h.UserContestHistory)
			// Listing & detail: optional auth so we can filter by group membership
			// when logged in, while still serving global contests to anonymous users.
			contests.GET("", authOptional, h.ListContests)
			contests.GET("/:id/problems/:slug", authRequired, activeUser, h.GetContestProblem)
			contests.GET("/:id", authOptional, h.GetContest)
			contests.GET("/:id/leaderboard", authOptional, h.ContestLeaderboard)
			contests.GET("/:id/ratings/predict", h.ContestRatingPredict)
			contests.GET("/:id/ratings/stream", h.ContestRatingStream)
			contests.POST("", authRequired, activeUser, h.CreateContest)
			contests.POST("/:id/problems/:slug/run", authRequired, activeUser, h.RunContestProblemSamples)
			contests.POST("/:id/submit", authRequired, activeUser, h.ContestSubmit)
			contests.POST("/:id/finalize", authRequired, activeUser, h.FinalizeContest)
			contests.POST("/:id/proctor-events", authRequired, activeUser, h.RecordProctorEvent)
		}

		// Submission routes (all protected)
		submissions := api.Group("/submissions")
		submissions.Use(authRequired, activeUser)
		{
			submissions.POST("", h.CreateSubmission)
			submissions.GET("/health", h.SubmissionsHealth)
			submissions.GET("/me", h.GetUserSubmissions)
			submissions.GET("/mine", h.ListMySubmissions) // full filtered history
			submissions.GET("/question/:slug", h.GetQuestionSubmissions)
			submissions.GET("/:id", h.GetSubmission)
		}

		// Groups — all members can list/search; joining requires auth
		groups := api.Group("/groups")
		groups.Use(authRequired, activeUser)
		{
			groups.GET("", h.ListGroups)
			// Static routes MUST be declared before parameterised ones.
			groups.GET("/my-requests", h.ListMyJoinRequests)
			groups.GET("/:id", h.GetGroup)
			groups.POST("/:id/join", h.RequestJoinGroup)
			groups.DELETE("/:id/join", h.CancelJoinRequest)
			groups.POST("/:id/leave", h.LeaveGroup)
		}

		// Sandbox routes (protected)
		sandbox := api.Group("/sandbox")
		sandbox.Use(authRequired, activeUser)
		{
			sandbox.POST("/run", h.RunCode)
			sandbox.GET("/health", h.SandboxHealth)
		}

		// ── Admin routes ──────────────────────────────────────────────
		admin := api.Group("/admin")
		admin.Use(authRequired, activeUser, middleware.RequireAdminSiteAccess(db))
		{
			// ── Dashboard ─────────────────────────────────────────────
			admin.GET("/dashboard", middleware.RequireSetterOrAbove(), h.AdminDashboard)

			// ── Problem management ────────────────────────────────────
			adminProblems := admin.Group("/problems")
			{
				adminProblems.GET("", h.AdminListProblems)
				adminProblems.POST("", h.AdminCreateProblem)
			}

			// ── Tag management (setters/admins pick and create tags) ──
			adminTags := admin.Group("/tags")
			{
				adminTags.GET("", h.AdminListTags)
				adminTags.POST("", h.AdminCreateTag)
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

				// Publish / unpublish (toggle student-facing visibility)
				adminProblem.POST("/publish", middleware.RequireProblemAccess(db, "editor"), h.AdminPublishProblem)
				adminProblem.POST("/unpublish", middleware.RequireProblemAccess(db, "editor"), h.AdminUnpublishProblem)

				// Access control
				adminProblem.GET("/access", middleware.RequireProblemAccess(db, "owner"), h.AdminListProblemAccess)
				adminProblem.POST("/access", middleware.RequireProblemAccess(db, "owner"), h.AdminGrantProblemAccess)
				adminProblem.DELETE("/access/:userId", middleware.RequireProblemAccess(db, "owner"), h.AdminRevokeProblemAccess)

				// Test cases
				adminProblem.GET("/tests", middleware.RequireProblemAccess(db, "viewer"), h.AdminListTests)
				adminProblem.POST("/tests", middleware.RequireProblemAccess(db, "editor"), h.AdminCreateTest)
				adminProblem.POST("/tests/bulk", middleware.RequireProblemAccess(db, "editor"), h.AdminBulkCreateTests)
				adminProblem.DELETE("/tests/bulk", middleware.RequireProblemAccess(db, "editor"), h.AdminBulkDeleteTests)
				adminProblem.PATCH("/tests/bulk", middleware.RequireProblemAccess(db, "editor"), h.AdminBulkPatchTests)
				adminProblem.PUT("/tests/:testId", middleware.RequireProblemAccess(db, "editor"), h.AdminUpdateTest)
				adminProblem.DELETE("/tests/:testId", middleware.RequireProblemAccess(db, "editor"), h.AdminDeleteTest)
				adminProblem.POST("/tests/:testId/run-solution", middleware.RequireProblemAccess(db, "editor"), h.AdminRunSolutionOnTest)
				adminProblem.POST("/tests/generate", middleware.RequireProblemAccess(db, "editor"), h.AdminGenerateTests)
				adminProblem.POST("/tests/validate", middleware.RequireProblemAccess(db, "editor"), h.AdminValidateTests)
				adminProblem.POST("/tests/reorder", middleware.RequireProblemAccess(db, "editor"), h.AdminReorderTests)

				// Generators
				adminProblem.GET("/generators", middleware.RequireProblemAccess(db, "viewer"), h.AdminListGenerators)
				adminProblem.POST("/generators", middleware.RequireProblemAccess(db, "editor"), h.AdminCreateGenerator)
				adminProblem.PUT("/generators/:compId", middleware.RequireProblemAccess(db, "editor"), h.AdminUpdateGenerator)
				adminProblem.DELETE("/generators/:compId", middleware.RequireProblemAccess(db, "editor"), h.AdminDeleteGenerator)
				adminProblem.POST("/generators/:compId/compile", middleware.RequireProblemAccess(db, "editor"), h.AdminCompileGenerator)
				adminProblem.POST("/generators/:compId/activate", middleware.RequireProblemAccess(db, "editor"), h.AdminActivateGenerator)

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
				adminContests.POST("/:id/rerun-rating", middleware.RequireAdmin(), h.AdminRerunContestRating)

				// Proctoring (contest-scoped)
				adminContests.GET("/:id/proctor-events", h.AdminListProctorEvents)
				adminContests.GET("/:id/proctor-summary", h.AdminProctorSummary)

				// Export
				adminContests.GET("/:id/export.csv", h.AdminExportContestCSV)

				// Plagiarism check (manual, synchronous)
				adminContests.POST("/:id/plag-check", h.AdminContestPlagCheck)
			}

			// ── Submission management (admin grading) ─────────────────
			adminSubmissions := admin.Group("/submissions")
			{
				adminSubmissions.GET("", h.AdminListSubmissions)
				adminSubmissions.GET("/:id", h.AdminGetSubmission)
				adminSubmissions.PUT("/:id/grade", h.AdminGradeSubmission)
				adminSubmissions.POST("/:id/run", h.AdminRunSubmission)
			}

			// ── User management (admin only) ──────────────────────────
			adminUsers := admin.Group("/users")
			adminUsers.Use(middleware.RequireAdmin())
			{
				adminUsers.GET("", h.AdminListUsers)
				adminUsers.GET("/:id", h.AdminGetUser)
				adminUsers.PUT("/:id/role", h.AdminUpdateUserRole)
				adminUsers.PUT("/:id/ban", h.AdminUpdateUserBan)
			}

			// ── Group management ─────────────────────────────────────
			// Creating/deleting groups is admin-only; group admins can
			// update and manage members inside a group.
			adminGroups := admin.Group("/groups")
			{
				adminGroups.POST("", middleware.RequireAdmin(), h.AdminCreateGroup)
				adminGroups.DELETE("/:id", middleware.RequireAdmin(), h.AdminDeleteGroup)
				adminGroups.PUT("/:id", h.AdminUpdateGroup)
				adminGroups.GET("/:id/requests", h.AdminListJoinRequests)
				adminGroups.POST("/:id/requests/:reqId/decide", h.AdminDecideJoinRequest)
				adminGroups.POST("/:id/members", h.AdminAddGroupMember)
				adminGroups.PUT("/:id/members/:userId", h.AdminUpdateGroupMember)
				adminGroups.DELETE("/:id/members/:userId", h.AdminRemoveGroupMember)
			}

			// ── Audit log (admin only) ────────────────────────────────
			admin.GET("/audit-log", middleware.RequireAdmin(), h.AdminGetAuditLog)
		}
	}

	return r
}
