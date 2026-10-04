package utils

import "time"

type AgentAccess struct {
	ID          string    `json:"id"`
	OwnerID     int64     `json:"owner_id"`
	PostID      int64     `json:"post_id"`
	Name        string    `json:"name"`
	Permissions []string  `json:"permissions"`
	ExpiresAt   time.Time `json:"expires_at"`
}
