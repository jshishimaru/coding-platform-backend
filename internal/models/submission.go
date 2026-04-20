package models

import (
	"encoding/json"
	"time"
)

type Submission struct {
	ID            int             `json:"id"`
	UserID        int             `json:"user_id"`
	ProblemID     *int            `json:"problem_id,omitempty"`
	ContestID     *int            `json:"contest_id,omitempty"`
	Language      string          `json:"language"`
	SourceCode    string          `json:"source_code"`
	Status        string          `json:"status"`
	RuntimeMs     *int            `json:"runtime_ms,omitempty"`
	MemoryKb      *int            `json:"memory_kb,omitempty"`
	PassedCount   int             `json:"passed_count"`
	TotalCount    int             `json:"total_count"`
	ResultDetails json.RawMessage `json:"result_details,omitempty"`
	SubmittedAt   time.Time       `json:"submitted_at"`
	ManualScore   *int            `json:"manual_score,omitempty"`
	Feedback      string          `json:"feedback,omitempty"`
	GradedBy      *int            `json:"graded_by,omitempty"`
	GradedAt      *time.Time      `json:"graded_at,omitempty"`
	IsLocked      bool            `json:"is_locked"`
}
