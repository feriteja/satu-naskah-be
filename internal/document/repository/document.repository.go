package repository

import (
	"database/sql"
	"satunaskah/internal/document/model"
	"satunaskah/pkg/logger"
	"time"
)

type DocumentRepository struct {
	DB *sql.DB
}

func NewDocumentRepository(db *sql.DB) *DocumentRepository {
	return &DocumentRepository{DB: db}
}

func (r *DocumentRepository) Create(id, content, ownerID, title string) error {
	_, err := r.DB.Exec(`INSERT INTO documents (id, content, updated_at, owner_id, title) VALUES ($1, $2, NOW(), $3, $4)`,
		id, content, ownerID, title)
	if err != nil {
		logger.Sugar.Errorf("Failed to create document: %v", err)
	}
	return err
}

func (r *DocumentRepository) GetOwnerID(docID string) (string, error) {
	var ownerID string
	err := r.DB.QueryRow("SELECT owner_id FROM documents WHERE id = $1", docID).Scan(&ownerID)
	if err != nil {
		logger.Sugar.Errorf("Failed to get owner ID for doc %s: %v", docID, err)
	}
	return ownerID, err
}

func (r *DocumentRepository) GetCollaboratorRole(docID, userID string) (string, error) {
	var role string
	err := r.DB.QueryRow("SELECT role FROM collaborators WHERE document_id = $1 AND user_id = $2", docID, userID).Scan(&role)
	if err != nil && err != sql.ErrNoRows {
		logger.Sugar.Errorf("Failed to get collaborator role: %v", err)
	}
	return role, err
}

func (r *DocumentRepository) UpdateContent(docID, content string) error {
	_, err := r.DB.Exec(`UPDATE documents SET content = $1, updated_at = NOW() WHERE id = $2`, content, docID)
	if err != nil {
		logger.Sugar.Errorf("Failed to update content for doc %s: %v", docID, err)
	}
	return err
}

func (r *DocumentRepository) Delete(docID string) error {
	_, err := r.DB.Exec("DELETE FROM documents WHERE id = $1", docID)
	if err != nil {
		logger.Sugar.Errorf("Failed to delete doc %s: %v", docID, err)
	}
	return err
}

func (r *DocumentRepository) UpdateTitle(docID, title, ownerID string) (int64, error) {
	result, err := r.DB.Exec("UPDATE documents SET title = $1, updated_at = NOW() WHERE id = $2 AND owner_id = $3", title, docID, ownerID)
	if err != nil {
		logger.Sugar.Errorf("Failed to update title for doc %s: %v", docID, err)
		return 0, err
	}
	return result.RowsAffected()
}

func (r *DocumentRepository) GetUserByEmail(email string) (string, error) {
	var userID string
	err := r.DB.QueryRow("SELECT id FROM auth.users WHERE email = $1", email).Scan(&userID)
	if err != nil {
		logger.Sugar.Errorf("Failed to get user by email %s: %v", email, err)
	}
	return userID, err
}

func (r *DocumentRepository) AddCollaborator(docID, userID, role string) error {
	_, err := r.DB.Exec(`INSERT INTO collaborators (document_id, user_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (document_id, user_id) DO UPDATE SET role = $3`, docID, userID, role)
	if err != nil {
		logger.Sugar.Errorf("Failed to add collaborator %s to doc %s: %v", userID, docID, err)
	}
	return err
}

func (r *DocumentRepository) GetDocumentsByUser(userID string) (*sql.Rows, error) {
	query := `
		SELECT id, title, updated_at, content, owner_id FROM documents WHERE owner_id = $1
		UNION
		SELECT d.id, d.title, d.updated_at, d.content, d.owner_id FROM documents d JOIN collaborators c ON d.id = c.document_id WHERE c.user_id = $1
		ORDER BY updated_at DESC`
	rows, err := r.DB.Query(query, userID)
	if err != nil {
		logger.Sugar.Errorf("Failed to get documents for user %s: %v", userID, err)
	}
	return rows, err
}

func (r *DocumentRepository) GetDocumentMembers(docID string) ([]model.CollaboratorInfo, error) {
	query := `
		SELECT u.id, u.email, 'owner' as role FROM documents d JOIN auth.users u ON d.owner_id = u.id WHERE d.id = $1
		UNION ALL
		SELECT u.id, u.email, c.role FROM collaborators c JOIN auth.users u ON c.user_id = u.id WHERE c.document_id = $1
	`
	rows, err := r.DB.Query(query, docID)
	if err != nil {
		logger.Sugar.Errorf("Failed to get document members for doc %s: %v", docID, err)
		return nil, err
	}
	defer rows.Close()

	var members []model.CollaboratorInfo
	for rows.Next() {
		var c model.CollaboratorInfo
		if err := rows.Scan(&c.ID, &c.Name, &c.Role); err == nil {
			members = append(members, c)
		}
	}
	return members, nil
}

func (r *DocumentRepository) AddComment(docID, userID, content, quote string, textRange interface{}) (string, time.Time, error) {
	var commentID string
	var createdAt time.Time
	err := r.DB.QueryRow(`
		INSERT INTO comments (document_id, user_id, content, quote, text_range, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		RETURNING id, created_at`,
		docID, userID, content, quote, textRange,
	).Scan(&commentID, &createdAt)
	if err != nil {
		logger.Sugar.Errorf("Failed to add comment to doc %s: %v", docID, err)
	}
	return commentID, createdAt, err
}

func (r *DocumentRepository) GetComments(docID string) ([]model.CommentResponse, error) {
	rows, err := r.DB.Query("SELECT id, document_id, user_id, content, quote, text_range, created_at, is_resolved FROM comments WHERE document_id = $1 ORDER BY created_at ASC", docID)
	if err != nil {
		logger.Sugar.Errorf("Failed to get comments for doc %s: %v", docID, err)
		return nil, err
	}
	defer rows.Close()

	var comments []model.CommentResponse
	for rows.Next() {
		var c model.CommentResponse
		if err := rows.Scan(&c.ID, &c.DocID, &c.UserID, &c.Content, &c.Quote, &c.TextRange, &c.CreatedAt, &c.Resolved); err != nil {
			continue
		}
		comments = append(comments, c)
	}
	return comments, nil
}

func (r *DocumentRepository) ResolveComment(commentID, userID string) (string, error) {
	var docID string
	err := r.DB.QueryRow(`
		UPDATE comments SET is_resolved = NOT is_resolved 
		WHERE id = $1 AND (user_id = $2 OR document_id IN (SELECT id FROM documents WHERE owner_id = $2))
		RETURNING document_id`, commentID, userID).Scan(&docID)
	if err != nil {
		logger.Sugar.Errorf("Failed to resolve comment %s: %v", commentID, err)
	}
	return docID, err
}

func (r *DocumentRepository) DeleteComment(commentID, userID string) (string, error) {
	var docID string
	err := r.DB.QueryRow(`
		DELETE FROM comments 
		WHERE id = $1 AND (user_id = $2 OR document_id IN (SELECT id FROM documents WHERE owner_id = $2))
		RETURNING document_id`, commentID, userID).Scan(&docID)
	if err != nil {
		logger.Sugar.Errorf("Failed to delete comment %s: %v", commentID, err)
	}
	return docID, err
}

func (r *DocumentRepository) CheckAccess(docID, userID string) (bool, error) {
	var hasAccess bool
	err := r.DB.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM documents WHERE id = $1 AND owner_id = $2
			UNION
			SELECT 1 FROM collaborators WHERE document_id = $1 AND user_id = $2
		)`, docID, userID).Scan(&hasAccess)
	if err != nil {
		logger.Sugar.Errorf("Failed to check access for user %s on doc %s: %v", userID, docID, err)
	}
	return hasAccess, err
}
