package socket

import (
	"database/sql"
	"encoding/json"
	"satunaskah/pkg/logger"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	UpdateType         = "UPDATE"          // Document text changes
	CursorType         = "CURSOR"          // User moved their mouse/cursor
	JoinType           = "JOIN"            // User opened the document
	LeaveType          = "LEAVE"           // User closed the tab
	PresenceUpdateType = "PRESENCE_UPDATE" // A user joined or left
	CommentType        = "COMMENT"         // New comment added
	CommentUpdateType  = "COMMENT_UPDATE"  // Comment resolved/edited
	CommentDeleteType  = "COMMENT_DELETE"  // Comment deleted
	MetadataType       = "METADATA"        // Document title/info

	RoleWriter   = "writer"
	RoleReviewer = "reviewer"
	RoleReader   = "reader"
)

type WSMessage struct {
	Type    string          `json:"type"`
	DocID   string          `json:"document_id"`
	UserID  string          `json:"user_id"`
	Payload json.RawMessage `json:"payload"`
}

type UserStatus struct {
	UserID    string    `json:"user_id"`
	CursorPos int       `json:"cursor_pos"` // Or a more complex {line, ch} object
	LastSeen  time.Time `json:"last_seen"`
}
type Hub struct {
	Rooms      map[string]map[*Client]bool
	Broadcast  chan WSMessage
	Register   chan *Client
	Unregister chan *Client
	db         *sql.DB
	// Track document state in memory
	DocumentCache map[string][]byte
	DirtyDocs     map[string]bool
	mu            sync.Mutex
	Presence      map[string]map[string]UserStatus // docID -> userID -> status
}

type Client struct {
	Hub    *Hub
	Conn   *websocket.Conn
	DocID  string
	UserID string
	Send   chan []byte
	Role   string // Store the user's role
	Title  string // Document title
}

func NewHub(db *sql.DB) *Hub {
	return &Hub{
		Rooms:         make(map[string]map[*Client]bool),
		Broadcast:     make(chan WSMessage),
		Register:      make(chan *Client),
		Unregister:    make(chan *Client),
		db:            db,
		DocumentCache: make(map[string][]byte),
		DirtyDocs:     make(map[string]bool),
		Presence:      make(map[string]map[string]UserStatus),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.Register:
			// 12. The Hub receives the new client from the `Register` channel (sent in step 11).
			h.mu.Lock()
			// Initialize room, presence, and load document if it's the first user.
			if h.Rooms[client.DocID] == nil {
				h.Rooms[client.DocID] = make(map[*Client]bool)
				h.Presence[client.DocID] = make(map[string]UserStatus)

				// If this is the first user in a room, the Hub loads the document content from the database.
				var content []byte
				err := h.db.QueryRow("SELECT content FROM documents WHERE id = $1", client.DocID).Scan(&content)
				if err != nil {
					logger.Sugar.Errorf("Failed to load document %s (or not found): %v", client.DocID, err)
					content = []byte(`{"ops":[]}`) // Default to empty content on failure
				}
				h.DocumentCache[client.DocID] = content
			}
			// The client is added to the room for their specific document.
			h.Rooms[client.DocID][client] = true

			// Add user to presence map.
			h.Presence[client.DocID][client.UserID] = UserStatus{UserID: client.UserID, LastSeen: time.Now()}

			// Get the current document content from the in-memory cache.
			currentContent := h.DocumentCache[client.DocID]
			h.mu.Unlock()

			// 13. The Hub sends the full, current document content directly to the new client so their editor is up-to-date.
			// Send the full document state to the user who just joined.
			initialMsgPayload, _ := json.Marshal(WSMessage{Type: UpdateType, DocID: client.DocID, Payload: json.RawMessage(currentContent)})
			client.Send <- initialMsgPayload

			// Send Metadata (Title)
			metaPayload, _ := json.Marshal(map[string]string{"title": client.Title})
			metaMsg, _ := json.Marshal(WSMessage{Type: MetadataType, DocID: client.DocID, UserID: client.UserID, Payload: json.RawMessage(metaPayload)})
			client.Send <- metaMsg

			// 14. The Hub broadcasts a "presence update" to all other clients in the room to let them know a new user has joined.
			// Notify everyone else in the room about the new user.
			h.broadcastPresenceUpdate(client.DocID)

		case client := <-h.Unregister:
			// 19. The Hub receives a client to unregister (sent in step 18).
			h.mu.Lock()
			docID := client.DocID // Store docID before client is gone
			if _, ok := h.Rooms[client.DocID][client]; ok {
				// The client is removed from the room and presence list.
				delete(h.Rooms[client.DocID], client)
				delete(h.Presence[client.DocID], client.UserID)
				close(client.Send)

				// If the room is empty, clean up all associated resources.
				if len(h.Rooms[client.DocID]) == 0 {
					if h.DirtyDocs[client.DocID] {
						_, err := h.db.Exec(`UPDATE documents SET content = $1, updated_at = NOW() WHERE id = $2`,
							h.DocumentCache[client.DocID], client.DocID,
						)
						if err != nil {
							logger.Sugar.Errorf("Failed to save doc %s on close: %v", client.DocID, err)
						}
					}
					delete(h.Rooms, client.DocID)
					delete(h.Presence, client.DocID)
					delete(h.DocumentCache, client.DocID)
					delete(h.DirtyDocs, client.DocID)
					logger.Sugar.Infof("Closed and cleaned up empty room: %s", client.DocID)
				}
			}
			h.mu.Unlock()

			// 20. A final presence update is sent to remaining users so the departed user's icon disappears from their screen.
			// Notify remaining users that someone left, only if the room still exists.
			if h.Rooms[docID] != nil {
				h.broadcastPresenceUpdate(docID)
			}

		case msg := <-h.Broadcast:
			// 17. The Hub receives a message to broadcast (sent in step 16).
			h.mu.Lock()
			// If it's a document update, save the content and mark for DB persistence.
			if msg.Type == UpdateType {
				h.DocumentCache[msg.DocID] = msg.Payload
				h.DirtyDocs[msg.DocID] = true
				// 21. The document is now "dirty". The SaveWorker (see below) will pick this up and save it to the database.
			}
			// For other types like CURSOR, we just broadcast without saving.

			// Marshal the message once to be sent to all clients.
			payload, err := json.Marshal(msg)
			if err != nil {
				logger.Sugar.Errorf("Error marshalling broadcast message: %v", err)
				h.mu.Unlock()
				continue
			}

			// It builds a list of clients who should receive this message (everyone in the room except the original sender).
			// Create a list of clients to send to, to avoid holding the lock during I/O.
			clientsToSend := make([]*Client, 0, len(h.Rooms[msg.DocID]))
			for client := range h.Rooms[msg.DocID] {
				if client.UserID != msg.UserID { // Don't send the message back to the sender.
					clientsToSend = append(clientsToSend, client)
				}
			}
			h.mu.Unlock()

			// The message is sent to the `Send` channel of each recipient client.
			// The client's `writePump` will handle writing it to the socket.
			// Broadcast message outside of the lock.
			for _, client := range clientsToSend {
				select {
				case client.Send <- payload:
				default:
					// If the send buffer is full, the client is lagging.
					// Unregister the client to prevent blocking the hub.
					logger.Sugar.Warnf("Client %s's send buffer is full. Unregistering.", client.UserID)
					h.Unregister <- client
				}
			}
		}
	}
}

func (h *Hub) SaveWorker() {
	// 22. This function runs in a separate goroutine, triggered every 10 seconds.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		type docData struct {
			Content []byte
			OwnerID string
		}
		docsToSave := make(map[string]docData)

		h.mu.Lock()
		// It finds all documents that have been marked as "dirty" (modified in memory).
		// Find all dirty docs and copy their content to save later.
		for docID, isDirty := range h.DirtyDocs {
			if isDirty {
				// Make a copy of the content to use outside the lock.
				contentCopy := make([]byte, len(h.DocumentCache[docID]))
				copy(contentCopy, h.DocumentCache[docID])

				// Try to find an owner from active clients to use if this is a new document
				var ownerID string
				if clients, ok := h.Rooms[docID]; ok {
					for client := range clients {
						ownerID = client.UserID
						break
					}
				}
				docsToSave[docID] = docData{Content: contentCopy, OwnerID: ownerID}
			}
		}
		h.mu.Unlock()

		// 23. It performs the database write operation. Using "INSERT ... ON CONFLICT" is an efficient "upsert" that creates the doc if it's new or updates it if it exists.
		// Perform database I/O without holding the hub's lock.
		for docID, data := range docsToSave {
			// Since documents are always created via the API, we only ever need to update them here.
			_, err := h.db.Exec(`UPDATE documents SET content = $1, updated_at = NOW() WHERE id = $2`, data.Content, docID)
			if err != nil {
				logger.Sugar.Errorf("Failed to save doc %s: %v", docID, err)
				continue // Leave the dirty flag as true, will retry on the next tick.
			}

			// Lock again to safely update the dirty flag.
			// 24. If the save was successful, it marks the document as "clean" again,
			//  so it won't be saved again on the next tick unless new changes arrive.
			h.mu.Lock()
			// Only mark as clean if the content hasn't changed again
			// since we started the save operation.
			if string(h.DocumentCache[docID]) == string(data.Content) {
				h.DirtyDocs[docID] = false
			}
			h.mu.Unlock()

			logger.Sugar.Infof("Auto-saved document: %s", docID)
		}
	}
}

// RemoveDocument forcefully removes a document from memory and disconnects clients.
// This is called when a document is deleted via the API.
func (h *Hub) RemoveDocument(docID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// 1. Remove from memory so it doesn't get auto-saved back to DB
	delete(h.DocumentCache, docID)
	delete(h.DirtyDocs, docID)
	delete(h.Presence, docID)

	// 2. Disconnect all clients currently in the room
	if clients, ok := h.Rooms[docID]; ok {
		for client := range clients {
			client.Conn.Close() // This will trigger the readPump to exit and unregister safely
		}
		delete(h.Rooms, docID)
	}
}

func (h *Hub) broadcastPresenceUpdate(docID string) {
	var userStatuses []UserStatus
	var clientsToSend []*Client

	h.mu.Lock()
	// Collect all data needed from the hub under a single lock
	if _, ok := h.Presence[docID]; ok {
		userStatuses = make([]UserStatus, 0, len(h.Presence[docID]))
		for _, status := range h.Presence[docID] {
			userStatuses = append(userStatuses, status)
		}

		clientsToSend = make([]*Client, 0, len(h.Rooms[docID]))
		for client := range h.Rooms[docID] {
			clientsToSend = append(clientsToSend, client)
		}
	}
	h.mu.Unlock()

	// If there are no clients, there's nothing to do
	if len(clientsToSend) == 0 {
		return
	}

	// Marshal the payload outside the lock
	payload, err := json.Marshal(userStatuses)
	if err != nil {
		logger.Sugar.Errorf("Error marshalling presence broadcast: %v", err)
		return
	}
	broadcastPayload, _ := json.Marshal(WSMessage{Type: PresenceUpdateType, DocID: docID, Payload: payload})

	for _, client := range clientsToSend {
		select {
		case client.Send <- broadcastPayload:
		default:
			// Don't unregister here, just log. The main pumps will handle unresponsive clients.
			logger.Sugar.Warnf("Client %s's send buffer was full during presence update.", client.UserID)
		}
	}
}
