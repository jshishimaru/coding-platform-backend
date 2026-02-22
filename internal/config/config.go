package config

import "os"

type Config struct {
	ServerPort string
	// PostgreSQL
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	// Redis
	RedisHost string
	RedisPort string
	// JWT
	JWTSecret      string
	JWTExpiryHours int
	// Frontend
	FrontendURL string
}

func Load() *Config {
	return &Config{
		ServerPort:     getEnv("PORT", "3000"),
		DBHost:         getEnv("DB_HOST", "coding-platform-postgres"),
		DBPort:         getEnv("DB_PORT", "5432"),
		DBUser:         getEnv("DB_USER", "postgres"),
		DBPassword:     getEnv("DB_PASSWORD", "postgres"),
		DBName:         getEnv("DB_NAME", "coding_platform"),
		RedisHost:      getEnv("REDIS_HOST", "coding-platform-redis"),
		RedisPort:      getEnv("REDIS_PORT", "6379"),
		JWTSecret:      getEnv("JWT_SECRET", "super-secret-change-me-in-production"),
		JWTExpiryHours: 72,
		FrontendURL:    getEnv("FRONTEND_URL", "http://localhost:8080"),
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}
