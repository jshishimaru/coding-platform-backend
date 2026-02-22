package main

import (
	"log"
	"os"

	"github.com/coding-platform/backend/internal/config"
	"github.com/coding-platform/backend/internal/database"
	"github.com/coding-platform/backend/internal/router"
)

func main() {
	// Load config
	cfg := config.Load()

	// Connect to PostgreSQL
	db, err := database.ConnectPostgres(cfg)
	if err != nil {
		log.Printf("WARNING: PostgreSQL connection failed: %v", err)
	} else {
		defer db.Close()
		log.Println("PostgreSQL connected")
	}

	// Connect to Redis
	rdb, err := database.ConnectRedis(cfg)
	if err != nil {
		log.Printf("WARNING: Redis connection failed: %v", err)
	} else {
		defer rdb.Close()
		log.Println("Redis connected")
	}

	// Setup router
	r := router.Setup(db, rdb, cfg)

	// Start server
	port := cfg.ServerPort
	if port == "" {
		port = "3000"
	}
	log.Printf("Backend server starting on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
		os.Exit(1)
	}
}
