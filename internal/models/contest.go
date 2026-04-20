package models

import "time"

type Contest struct {
	ID              int       `json:"id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	StartTime       time.Time `json:"start_time"`
	EndTime         time.Time `json:"end_time"`
	IsRated         bool      `json:"is_rated"`
	CreatedBy       int       `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	GroupID         *int      `json:"group_id,omitempty"`
	GroupName       string    `json:"group_name,omitempty"`
	Proctored       bool      `json:"proctored"`
	GradeVisibility string    `json:"grade_visibility"`
}
