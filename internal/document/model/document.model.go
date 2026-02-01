package model

import (
	"encoding/json"
	"time"
)

type CreateDocResponse struct {
	DocID string `json:"document_id"`
}

type CollaboratorInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Avatar string `json:"avatar,omitempty"`
}

type DocumentMetadata struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	UpdatedAt time.Time          `json:"updated_at"`
	Snippet   string             `json:"snippet"`
	IsOwner   bool               `json:"is_owner"`
	Collab    []CollaboratorInfo `json:"collab"`
}

type CreateDocRequest struct {
	Title string `json:"title"`
}

type UpdateDocRequest struct {
	Title string `json:"title"`
}

type InviteRequest struct {
	DocID string `json:"document_id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

type SaveDocRequest struct {
	DocID   string          `json:"document_id"`
	Content json.RawMessage `json:"content"`
}

type CommentRequest struct {
	DocID     string          `json:"document_id"`
	Content   string          `json:"content"`
	Quote     string          `json:"quote"`
	TextRange json.RawMessage `json:"text_range"` // JSON {index, length}
}

type CommentResponse struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	Resolved  bool      `json:"resolved"`
	CommentRequest
}

type MemberResponse struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}
