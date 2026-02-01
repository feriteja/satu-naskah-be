package handler

import (
	"encoding/json"
	"net/http"
	"satunaskah/internal/document/model"
	"satunaskah/internal/document/service"
	"satunaskah/middleware"
	"satunaskah/pkg/logger"
)

type DocumentHandler struct {
	Service *service.DocumentService
}

func NewDocumentHandler(service *service.DocumentService) *DocumentHandler {
	return &DocumentHandler{Service: service}
}

func (h *DocumentHandler) CreateDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	var req model.CreateDocRequest
	_ = json.NewDecoder(r.Body).Decode(&req) // Ignore error, default to empty

	docID, err := h.Service.CreateDocument(userID, req.Title)
	if err != nil {
		logger.Sugar.Errorf("Handler: Failed to create document: %v", err)
		http.Error(w, "Failed to create document: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.CreateDocResponse{DocID: docID})
}

func (h *DocumentHandler) SaveDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req model.SaveDocRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.Content) == 0 || string(req.Content) == "null" {
		http.Error(w, "Content cannot be empty", http.StatusBadRequest)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	if err := h.Service.SaveDocument(userID, req); err != nil {
		logger.Sugar.Errorf("Error saving document: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

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

	if err := h.Service.DeleteDocument(docID, userID); err != nil {
		logger.Sugar.Errorf("Handler: Failed to delete document %s: %v", docID, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

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

	var req model.UpdateDocRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Title == "" {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.Service.UpdateTitle(docID, userID, req.Title); err != nil {
		logger.Sugar.Errorf("Handler: Failed to update title for doc %s: %v", docID, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

	var req model.InviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Role != "writer" && req.Role != "reviewer" && req.Role != "reader" {
		http.Error(w, "Invalid role. Must be writer, reviewer, or reader", http.StatusBadRequest)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	if err := h.Service.InviteCollaborator(userID, req); err != nil {
		logger.Sugar.Errorf("Handler: Failed to invite collaborator: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

	docs, err := h.Service.GetDocuments(userID)
	if err != nil {
		logger.Sugar.Errorf("Error fetching documents: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(docs)
}

func (h *DocumentHandler) AddComment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req model.CommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.DocID == "" || req.Content == "" {
		http.Error(w, "Document ID and Content are required", http.StatusBadRequest)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	resp, err := h.Service.AddComment(userID, req)
	if err != nil {
		logger.Sugar.Errorf("Failed to add comment: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

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

	comments, err := h.Service.Repo.GetComments(docID)
	if err != nil {
		logger.Sugar.Errorf("Error fetching comments: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(comments)
}

func (h *DocumentHandler) ResolveComment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	commentID := r.URL.Query().Get("commentId")
	if commentID == "" {
		http.Error(w, "Missing commentId parameter", http.StatusBadRequest)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	if err := h.Service.ResolveComment(commentID, userID); err != nil {
		logger.Sugar.Errorf("Handler: Failed to resolve comment %s: %v", commentID, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Comment status updated"))
}

func (h *DocumentHandler) DeleteComment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	commentID := r.URL.Query().Get("commentId")
	if commentID == "" {
		http.Error(w, "Missing commentId parameter", http.StatusBadRequest)
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	if err := h.Service.DeleteComment(commentID, userID); err != nil {
		logger.Sugar.Errorf("Handler: Failed to delete comment %s: %v", commentID, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Comment deleted"))
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

	hasAccess, err := h.Service.Repo.CheckAccess(docID, userID)
	if err != nil || !hasAccess {
		http.Error(w, "Unauthorized or document not found", http.StatusForbidden)
		return
	}

	members, err := h.Service.Repo.GetDocumentMembers(docID)
	if err != nil {
		logger.Sugar.Errorf("Error fetching members: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(members)
}
