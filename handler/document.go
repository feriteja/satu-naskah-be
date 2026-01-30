package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"satunaskah/middleware"
	"satunaskah/socket"
	"time"
)

type DocumentHandler struct {
	DB  *sql.DB
	Hub *socket.Hub
}

type CreateDocResponse struct {
	DocID string `json:"document_id"`
}

type DocumentMetadata struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
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
	CommentRequest
}

type MemberResponse struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

func (h *DocumentHandler) CreateDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	// Generate a random DocID
	docID := generateDocID()
	if docID == "" {
		http.Error(w, "Failed to generate document ID", http.StatusInternalServerError)
		return
	}

	// Parse optional title from body
	var req CreateDocRequest
	_ = json.NewDecoder(r.Body).Decode(&req) // Ignore error, default to empty
	if req.Title == "" {
		req.Title = "Untitled Document"
	}

	// Insert new document with empty content "[]" (Quill Delta format)
	// We set the owner_id to the current user
	_, err := h.DB.Exec(`INSERT INTO documents (id, content, updated_at, owner_id, title) VALUES ($1, $2, NOW(), $3, $4)`,
		docID, string(`{"ops":[]}`), userID, req.Title)

	if err != nil {
		http.Error(w, "Failed to create document: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CreateDocResponse{DocID: docID})
}

func (h *DocumentHandler) SaveDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SaveDocRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	// 1. Check Permissions (Must be Owner or Writer)
	role := "reader" // Default
	var ownerID string
	err := h.DB.QueryRow("SELECT owner_id FROM documents WHERE id = $1", req.DocID).Scan(&ownerID)
	if err == sql.ErrNoRows {
		http.Error(w, "Document not found", http.StatusNotFound)
		return
	} else if err == nil && ownerID == userID {
		role = "writer"
	} else {
		var dbRole string
		if err := h.DB.QueryRow("SELECT role FROM collaborators WHERE document_id = $1 AND user_id = $2", req.DocID, userID).Scan(&dbRole); err == nil {
			role = dbRole
		}
	}

	if role != "writer" {
		http.Error(w, "Unauthorized: Only writers can save this document", http.StatusForbidden)
		return
	}

	// 2. Update Database
	_, err = h.DB.Exec(`UPDATE documents SET content = $1, updated_at = NOW() WHERE id = $2`, string(req.Content), req.DocID)
	if err != nil {
		log.Printf("Error saving document via API: %v", err)
		http.Error(w, "Failed to save document", http.StatusInternalServerError)
		return
	}

	// 3. Update Hub Cache and Broadcast to other clients
	msg := socket.WSMessage{
		Type:    socket.UpdateType,
		DocID:   req.DocID,
		UserID:  userID, // The user who triggered the save
		Payload: req.Content,
	}
	// This will update the hub's cache and send the new content to all other connected clients.
	h.Hub.Broadcast <- msg

	log.Printf("Document %s saved successfully by user %s via API", req.DocID, userID)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Document saved successfully"))
}

func (h *DocumentHandler) DeleteDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	docID := r.URL.Query().Get("docId")
	if docID == "" {
		http.Error(w, "Missing docId parameter", http.StatusBadRequest)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	// 1. Verify ownership
	var ownerID string
	err := h.DB.QueryRow("SELECT owner_id FROM documents WHERE id = $1", docID).Scan(&ownerID)
	if err == sql.ErrNoRows {
		http.Error(w, "Document not found", http.StatusNotFound)
		return
	} else if err != nil {
		log.Printf("Error checking ownership: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	if ownerID != userID {
		http.Error(w, "Unauthorized: You are not the owner of this document", http.StatusForbidden)
		return
	}

	// 2. Delete from Database
	_, err = h.DB.Exec("DELETE FROM documents WHERE id = $1", docID)
	if err != nil {
		http.Error(w, "Failed to delete document", http.StatusInternalServerError)
		return
	}

	// 3. Remove from Hub (disconnect active users and clear cache)
	h.Hub.RemoveDocument(docID)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Document deleted successfully"))
}

func (h *DocumentHandler) UpdateDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	docID := r.URL.Query().Get("docId")
	if docID == "" {
		http.Error(w, "Missing docId parameter", http.StatusBadRequest)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	var req UpdateDocRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Title == "" {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Update title, ensuring ownership
	result, err := h.DB.Exec("UPDATE documents SET title = $1, updated_at = NOW() WHERE id = $2 AND owner_id = $3", req.Title, docID, userID)
	if err != nil {
		log.Printf("Error updating document: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "Document not found or unauthorized", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Document updated successfully"))
}

func (h *DocumentHandler) AddCollaborator(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req InviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Role != "writer" && req.Role != "reviewer" && req.Role != "reader" {
		http.Error(w, "Invalid role. Must be writer, reviewer, or reader", http.StatusBadRequest)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	// 1. Verify ownership (Only owner can invite)
	var ownerID string
	err := h.DB.QueryRow("SELECT owner_id FROM documents WHERE id = $1", req.DocID).Scan(&ownerID)
	if err == sql.ErrNoRows {
		http.Error(w, "Document not found", http.StatusNotFound)
		return
	} else if err != nil {
		log.Printf("Error checking document owner: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	if ownerID != userID {
		http.Error(w, "Unauthorized: Only the owner can invite collaborators", http.StatusForbidden)
		return
	}

	// 2. Find Target User ID by Email (Directly from Supabase auth.users)
	var targetUserID string
	// Note: Ensure your database user has permission to SELECT from the 'auth' schema.
	err = h.DB.QueryRow("SELECT id FROM auth.users WHERE email = $1", req.Email).Scan(&targetUserID)
	if err == sql.ErrNoRows {
		http.Error(w, "User not found with that email", http.StatusNotFound)
		return
	}

	// 3. Upsert Collaborator
	_, err = h.DB.Exec(`INSERT INTO collaborators (document_id, user_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (document_id, user_id) DO UPDATE SET role = $3`, req.DocID, targetUserID, req.Role)
	if err != nil {
		log.Printf("Error adding collaborator: %v", err)
		http.Error(w, "Failed to add collaborator", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Collaborator added successfully"))
}

func (h *DocumentHandler) GetDocuments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	// Query documents owned by the user, ordered by most recently updated
	// Updated to include documents where the user is a collaborator
	query := `
		SELECT id, title, updated_at FROM documents WHERE owner_id = $1
		UNION
		SELECT d.id, d.title, d.updated_at FROM documents d JOIN collaborators c ON d.id = c.document_id WHERE c.user_id = $1
		ORDER BY updated_at DESC`
	rows, err := h.DB.Query(query, userID)
	if err != nil {
		log.Printf("Error fetching documents: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	docs := []DocumentMetadata{}
	for rows.Next() {
		var doc DocumentMetadata
		if err := rows.Scan(&doc.ID, &doc.Title, &doc.UpdatedAt); err != nil {
			continue
		}
		docs = append(docs, doc)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(docs)
}

func (h *DocumentHandler) AddComment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	// 1. Check Permissions (Must be Owner, Writer, or Reviewer)
	role := "reader" // Default
	var ownerID string
	err := h.DB.QueryRow("SELECT owner_id FROM documents WHERE id = $1", req.DocID).Scan(&ownerID)
	if err == nil && ownerID == userID {
		role = "writer" // Owner is effectively a writer
	} else {
		var dbRole string
		if err := h.DB.QueryRow("SELECT role FROM collaborators WHERE document_id = $1 AND user_id = $2", req.DocID, userID).Scan(&dbRole); err == nil {
			role = dbRole
		}
	}

	if role != "writer" && role != "reviewer" {
		http.Error(w, "Unauthorized: Only writers and reviewers can comment", http.StatusForbidden)
		return
	}

	// Handle TextRange for JSONB compatibility (lib/pq requires string for JSONB, not []byte)
	var textRange interface{} = nil
	if len(req.TextRange) > 0 {
		textRange = string(req.TextRange)
	}

	// 2. Insert into Database
	var commentID string
	var createdAt time.Time
	err = h.DB.QueryRow(`
		INSERT INTO comments (document_id, user_id, content, quote, text_range, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		RETURNING id, created_at`,
		req.DocID, userID, req.Content, req.Quote, textRange,
	).Scan(&commentID, &createdAt)

	if err != nil {
		http.Error(w, "Failed to save comment: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 3. Broadcast to WebSocket (Real-time update)
	// We construct the response object to send back to the API caller AND broadcast to others
	resp := CommentResponse{
		ID:             commentID,
		UserID:         userID,
		CreatedAt:      createdAt,
		CommentRequest: req,
	}

	payloadBytes, _ := json.Marshal(resp)
	msg := socket.WSMessage{
		Type:    socket.CommentType,
		DocID:   req.DocID,
		UserID:  userID,
		Payload: json.RawMessage(payloadBytes),
	}

	// Send to the Hub to distribute to all connected clients in the room
	h.Hub.Broadcast <- msg

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *DocumentHandler) GetComments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	docID := r.URL.Query().Get("docId")
	if docID == "" {
		http.Error(w, "Missing docId parameter", http.StatusBadRequest)
		return
	}

	rows, err := h.DB.Query("SELECT id, document_id, user_id, content, quote, text_range, created_at FROM comments WHERE document_id = $1 ORDER BY created_at ASC", docID)
	if err != nil {
		log.Printf("Error fetching comments: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	comments := []CommentResponse{}
	for rows.Next() {
		var c CommentResponse
		if err := rows.Scan(&c.ID, &c.DocID, &c.UserID, &c.Content, &c.Quote, &c.TextRange, &c.CreatedAt); err != nil {
			continue
		}
		comments = append(comments, c)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(comments)
}

func (h *DocumentHandler) GetDocumentMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	docID := r.URL.Query().Get("docId")
	if docID == "" {
		http.Error(w, "Missing docId parameter", http.StatusBadRequest)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	// 1. Check Permissions (Must be Owner or Collaborator to see members)
	var hasAccess bool
	err := h.DB.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM documents WHERE id = $1 AND owner_id = $2
			UNION
			SELECT 1 FROM collaborators WHERE document_id = $1 AND user_id = $2
		)`, docID, userID).Scan(&hasAccess)

	if err != nil || !hasAccess {
		http.Error(w, "Unauthorized or document not found", http.StatusForbidden)
		return
	}

	// 2. Fetch Members (Owner + Collaborators)
	// Note: This assumes the DB user has access to auth.users
	query := `
		SELECT u.id, u.email, 'owner' as role FROM documents d JOIN auth.users u ON d.owner_id = u.id WHERE d.id = $1
		UNION ALL
		SELECT u.id, u.email, c.role FROM collaborators c JOIN auth.users u ON c.user_id = u.id WHERE c.document_id = $1
	`
	rows, err := h.DB.Query(query, docID)
	if err != nil {
		log.Printf("Error fetching members: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	members := []MemberResponse{}
	for rows.Next() {
		var m MemberResponse
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role); err != nil {
			continue
		}
		members = append(members, m)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(members)
}

func generateDocID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
