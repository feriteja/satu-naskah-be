package service

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"satunaskah/internal/document/model"
	"satunaskah/internal/document/repository"
	"satunaskah/socket"
	"strings"
)

type DocumentService struct {
	Repo *repository.DocumentRepository
	Hub  *socket.Hub
}

func NewDocumentService(repo *repository.DocumentRepository, hub *socket.Hub) *DocumentService {
	return &DocumentService{Repo: repo, Hub: hub}
}

func (s *DocumentService) CreateDocument(userID, title string) (string, error) {
	docID := generateDocID()
	if docID == "" {
		return "", errors.New("failed to generate document ID")
	}
	if title == "" {
		title = "Untitled Document"
	}
	err := s.Repo.Create(docID, `{"ops":[]}`, userID, title)
	return docID, err
}

func (s *DocumentService) SaveDocument(userID string, req model.SaveDocRequest) error {
	// Permission Check
	role, err := s.getUserRole(req.DocID, userID)
	if err != nil {
		return err
	}
	if role != "writer" {
		return errors.New("unauthorized: only writers can save")
	}

	// Update DB
	if err := s.Repo.UpdateContent(req.DocID, string(req.Content)); err != nil {
		return err
	}

	// Broadcast
	s.Hub.Broadcast <- socket.WSMessage{
		Type:    socket.UpdateType,
		DocID:   req.DocID,
		UserID:  userID,
		Payload: req.Content,
	}
	return nil
}

func (s *DocumentService) DeleteDocument(docID, userID string) error {
	ownerID, err := s.Repo.GetOwnerID(docID)
	if err != nil {
		return err
	}
	if ownerID != userID {
		return errors.New("unauthorized: only owner can delete")
	}

	if err := s.Repo.Delete(docID); err != nil {
		return err
	}
	s.Hub.RemoveDocument(docID)
	return nil
}

func (s *DocumentService) UpdateTitle(docID, userID, title string) error {
	rowsAffected, err := s.Repo.UpdateTitle(docID, title, userID)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return errors.New("document not found or unauthorized")
	}
	return nil
}

func (s *DocumentService) InviteCollaborator(userID string, req model.InviteRequest) error {
	ownerID, err := s.Repo.GetOwnerID(req.DocID)
	if err != nil {
		return err
	}
	if ownerID != userID {
		return errors.New("unauthorized: only owner can invite")
	}

	targetUserID, err := s.Repo.GetUserByEmail(req.Email)
	if err != nil {
		return errors.New("user not found with that email")
	}

	return s.Repo.AddCollaborator(req.DocID, targetUserID, req.Role)
}

func (s *DocumentService) GetDocuments(userID string) ([]model.DocumentMetadata, error) {
	rows, err := s.Repo.GetDocumentsByUser(userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []model.DocumentMetadata
	for rows.Next() {
		var doc model.DocumentMetadata
		var content string
		var ownerID string
		if err := rows.Scan(&doc.ID, &doc.Title, &doc.UpdatedAt, &content, &ownerID); err != nil {
			continue
		}
		doc.IsOwner = (ownerID == userID)
		doc.Snippet = getSnippetFromContent(content)

		// Fetch collaborators
		members, _ := s.Repo.GetDocumentMembers(doc.ID)
		doc.Collab = members
		if doc.Collab == nil {
			doc.Collab = []model.CollaboratorInfo{}
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func (s *DocumentService) AddComment(userID string, req model.CommentRequest) (*model.CommentResponse, error) {
	role, err := s.getUserRole(req.DocID, userID)
	if err != nil {
		return nil, err
	}
	if role != "writer" && role != "reviewer" {
		return nil, errors.New("unauthorized")
	}

	var textRange interface{}
	if len(req.TextRange) > 0 {
		textRange = string(req.TextRange)
	}

	commentID, createdAt, err := s.Repo.AddComment(req.DocID, userID, req.Content, req.Quote, textRange)
	if err != nil {
		return nil, err
	}

	resp := &model.CommentResponse{
		ID:             commentID,
		UserID:         userID,
		CreatedAt:      createdAt,
		Resolved:       false,
		CommentRequest: req,
	}

	payloadBytes, _ := json.Marshal(resp)
	s.Hub.Broadcast <- socket.WSMessage{
		Type:    socket.CommentType,
		DocID:   req.DocID,
		UserID:  userID,
		Payload: json.RawMessage(payloadBytes),
	}
	return resp, nil
}

func (s *DocumentService) ResolveComment(commentID, userID string) error {
	docID, err := s.Repo.ResolveComment(commentID, userID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]interface{}{"id": commentID})
	s.Hub.Broadcast <- socket.WSMessage{Type: socket.CommentUpdateType, DocID: docID, UserID: userID, Payload: payload}
	return nil
}

func (s *DocumentService) DeleteComment(commentID, userID string) error {
	docID, err := s.Repo.DeleteComment(commentID, userID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"id": commentID})
	s.Hub.Broadcast <- socket.WSMessage{Type: socket.CommentDeleteType, DocID: docID, UserID: userID, Payload: payload}
	return nil
}

func (s *DocumentService) getUserRole(docID, userID string) (string, error) {
	ownerID, err := s.Repo.GetOwnerID(docID)
	if err == nil && ownerID == userID {
		return "writer", nil
	}
	role, err := s.Repo.GetCollaboratorRole(docID, userID)
	if err == nil {
		return role, nil
	}
	return "reader", nil // Default or error
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

func getSnippetFromContent(contentJSON string) string {
	type QuillOp struct {
		Insert interface{} `json:"insert"`
	}
	type QuillDelta struct {
		Ops []QuillOp `json:"ops"`
	}
	var delta QuillDelta
	if err := json.Unmarshal([]byte(contentJSON), &delta); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, op := range delta.Ops {
		if str, ok := op.Insert.(string); ok {
			sb.WriteString(str)
		}
		if sb.Len() > 100 {
			break
		}
	}
	res := strings.TrimSpace(sb.String())
	res = strings.ReplaceAll(res, "\n", " ")
	if len(res) > 100 {
		return res[:100] + "..."
	}
	return res
}
