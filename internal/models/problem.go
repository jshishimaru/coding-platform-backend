package models

import "time"

type Problem struct {
	ID            int       `json:"id"`
	Title         string    `json:"title"`
	Slug          string    `json:"slug"`
	Statement     string    `json:"statement"`
	Difficulty    string    `json:"difficulty"`
	TimeLimitMs   int       `json:"time_limit_ms"`
	MemoryLimitMb int       `json:"memory_limit_mb"`
	CreatedBy     int       `json:"created_by"`
	ContestID     *int      `json:"contest_id,omitempty"`
	Points        *int      `json:"points,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	// Populated via joins
	Tags []string `json:"tags,omitempty"`
}
