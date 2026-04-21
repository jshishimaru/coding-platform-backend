package models

import "time"

type User struct {
	ID           int       `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	Rating       int       `json:"rating"`
	IsBanned     bool      `json:"is_banned"`
	CreatedAt    time.Time `json:"created_at"`
}
