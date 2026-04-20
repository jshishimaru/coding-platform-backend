package models

import "time"

type Group struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedBy   int       `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`

	// Populated via joins
	MemberCount int    `json:"member_count,omitempty"`
	MyRole      string `json:"my_role,omitempty"` // role of the current user in this group
	IsMember    bool   `json:"is_member,omitempty"`
}

type GroupMember struct {
	GroupID  int       `json:"group_id"`
	UserID   int       `json:"user_id"`
	Username string    `json:"username,omitempty"`
	Role     string    `json:"role"` // 'member' or 'admin'
	JoinedAt time.Time `json:"joined_at"`
}

type GroupJoinRequest struct {
	ID         int        `json:"id"`
	GroupID    int        `json:"group_id"`
	GroupName  string     `json:"group_name,omitempty"`
	UserID     int        `json:"user_id"`
	Username   string     `json:"username,omitempty"`
	Status     string     `json:"status"` // 'pending', 'approved', 'rejected'
	Message    string     `json:"message"`
	DecidedBy  *int       `json:"decided_by,omitempty"`
	DecidedAt  *time.Time `json:"decided_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}
