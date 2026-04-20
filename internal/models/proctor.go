package models

import (
	"encoding/json"
	"time"
)

type ProctorEvent struct {
	ID        int             `json:"id"`
	ContestID int             `json:"contest_id"`
	UserID    int             `json:"user_id"`
	Username  string          `json:"username,omitempty"`
	EventType string          `json:"event_type"`
	Details   json.RawMessage `json:"details,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}
