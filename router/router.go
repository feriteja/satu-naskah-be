package router

import (
	"database/sql"
	"net/http"
	docHandler "satunaskah/internal/document"
	"satunaskah/internal/document/repository"
	"satunaskah/internal/document/service"
	"satunaskah/middleware"
	"satunaskah/socket"
)

func Setup(db *sql.DB, hub *socket.Hub) http.Handler {
	mux := http.NewServeMux()

	// WebSocket
	wsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Context().Value(middleware.UserIDKey).(string)
		socket.ServeWs(hub, w, r, userID)
	})
	mux.Handle("/ws", middleware.AuthMiddleware(wsHandler))

	// REST API
	docRepo := repository.NewDocumentRepository(db)
	docService := service.NewDocumentService(docRepo, hub)
	docHandler := docHandler.NewDocumentHandler(docService)
	auth := middleware.AuthMiddleware

	mux.Handle("/api/documents/create", auth(http.HandlerFunc(docHandler.CreateDocument)))
	mux.Handle("/api/documents/delete", auth(http.HandlerFunc(docHandler.DeleteDocument)))
	mux.Handle("/api/documents/update", auth(http.HandlerFunc(docHandler.UpdateDocument)))
	mux.Handle("/api/documents", auth(http.HandlerFunc(docHandler.GetDocuments)))
	mux.Handle("/api/documents/invite", auth(http.HandlerFunc(docHandler.AddCollaborator)))
	mux.Handle("/api/documents/comments/add", auth(http.HandlerFunc(docHandler.AddComment)))
	mux.Handle("/api/documents/comments", auth(http.HandlerFunc(docHandler.GetComments)))
	mux.Handle("/api/documents/comments/resolve", auth(http.HandlerFunc(docHandler.ResolveComment)))
	mux.Handle("/api/documents/comments/delete", auth(http.HandlerFunc(docHandler.DeleteComment)))
	mux.Handle("/api/documents/members", auth(http.HandlerFunc(docHandler.GetDocumentMembers)))
	mux.Handle("/api/documents/save", auth(http.HandlerFunc(docHandler.SaveDocument)))

	return middleware.CORSMiddleware(mux)
}
