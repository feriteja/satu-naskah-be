package store

import "time"

type Document struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"` // Store as JSON string or byte array
	OwnerID   string    `json:"owner_id"`
	UpdatedAt time.Time `json:"updated_at"`
}
