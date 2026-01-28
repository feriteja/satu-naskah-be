package socket

import (
	"database/sql"
	"encoding/json"
	"log"
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
)

type WSMessage struct {
	Type    string          `json:"type"`
	DocID   string          `json:"doc_id"`
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
	db         *sql.DB // Add database connection
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
			h.mu.Lock()
			// Initialize room, presence, and load document if it's the first user.
			if h.Rooms[client.DocID] == nil {
				h.Rooms[client.DocID] = make(map[*Client]bool)
				h.Presence[client.DocID] = make(map[string]UserStatus)

				var content []byte
				err := h.db.QueryRow("SELECT content FROM documents WHERE id = $1", client.DocID).Scan(&content)
				if err != nil && err != sql.ErrNoRows {
					log.Printf("Failed to load document %s from DB: %v", client.DocID, err)
					content = []byte("[]") // Default to empty content on failure
				}
				h.DocumentCache[client.DocID] = content
			}
			h.Rooms[client.DocID][client] = true

			// Add user to presence map.
			h.Presence[client.DocID][client.UserID] = UserStatus{UserID: client.UserID, LastSeen: time.Now()}

			// Get current content to send to the new client.
			currentContent := h.DocumentCache[client.DocID]
			h.mu.Unlock()

			// Send the full document state to the user who just joined.
			initialMsgPayload, _ := json.Marshal(WSMessage{Type: UpdateType, DocID: client.DocID, Payload: json.RawMessage(currentContent)})
			client.Send <- initialMsgPayload

			// Notify everyone else in the room about the new user.
			h.broadcastPresenceUpdate(client.DocID)

		case client := <-h.Unregister:
			h.mu.Lock()
			docID := client.DocID // Store docID before client is gone
			if _, ok := h.Rooms[client.DocID][client]; ok {
				delete(h.Rooms[client.DocID], client)
				delete(h.Presence[client.DocID], client.UserID)
				close(client.Send)

				// If the room is empty, clean up all associated resources.
				if len(h.Rooms[client.DocID]) == 0 {
					delete(h.Rooms, client.DocID)
					delete(h.Presence, client.DocID)
					delete(h.DocumentCache, client.DocID)
					delete(h.DirtyDocs, client.DocID)
					log.Printf("Closed and cleaned up empty room: %s", client.DocID)
				}
			}
			h.mu.Unlock()

			// Notify remaining users that someone left, only if the room still exists.
			if h.Rooms[docID] != nil {
				h.broadcastPresenceUpdate(docID)
			}

		case msg := <-h.Broadcast:
			h.mu.Lock()
			// If it's a document update, save the content and mark for DB persistence.
			if msg.Type == UpdateType {
				h.DocumentCache[msg.DocID] = msg.Payload
				h.DirtyDocs[msg.DocID] = true
			}
			// For other types like CURSOR, we just broadcast without saving.

			// Marshal the message once to be sent to all clients.
			payload, err := json.Marshal(msg)
			if err != nil {
				log.Printf("Error marshalling broadcast message: %v", err)
				h.mu.Unlock()
				continue
			}

			// Create a list of clients to send to, to avoid holding the lock during I/O.
			clientsToSend := make([]*Client, 0, len(h.Rooms[msg.DocID]))
			for client := range h.Rooms[msg.DocID] {
				if client.UserID != msg.UserID {
					clientsToSend = append(clientsToSend, client)
				}
			}
			h.mu.Unlock()

			// Broadcast message outside of the lock.
			for _, client := range clientsToSend {
				select {
				case client.Send <- payload:
				default:
					// If the send buffer is full, the client is lagging.
					// Unregister the client to prevent blocking the hub.
					log.Printf("Client %s's send buffer is full. Unregistering.", client.UserID)
					h.Unregister <- client
				}
			}
		}
	}
}

func (h *Hub) SaveWorker() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		type docData struct {
			Content []byte
			OwnerID string
		}
		docsToSave := make(map[string]docData)

		h.mu.Lock()
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

		// Perform database I/O without holding the hub's lock.
		for docID, data := range docsToSave {
			var err error
			if data.OwnerID != "" {
				// We have an owner, so we can Insert (if new) or Update.
				_, err = h.db.Exec(`
					INSERT INTO documents (id, content, updated_at, owner_id) VALUES ($2, $1, NOW(), $3)
					ON CONFLICT (id) DO UPDATE SET content = $1, updated_at = NOW()`,
					data.Content, docID, data.OwnerID,
				)
			} else {
				// No owner found (everyone left). We can ONLY update existing docs.
				// We cannot insert a new document because owner_id is required.
				_, err = h.db.Exec(`UPDATE documents SET content = $1, updated_at = NOW() WHERE id = $2`, data.Content, docID)
			}

			if err != nil {
				log.Printf("Failed to save doc %s: %v", docID, err)
				continue // Leave the dirty flag as true, will retry on the next tick.
			}

			// Lock again to safely update the dirty flag.
			h.mu.Lock()
			// Only mark as clean if the content hasn't changed again
			// since we started the save operation.
			if string(h.DocumentCache[docID]) == string(data.Content) {
				h.DirtyDocs[docID] = false
			}
			h.mu.Unlock()

			log.Printf("Auto-saved document: %s", docID)
		}
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
		log.Printf("Error marshalling presence broadcast: %v", err)
		return
	}
	broadcastPayload, _ := json.Marshal(WSMessage{Type: PresenceUpdateType, DocID: docID, Payload: payload})

	for _, client := range clientsToSend {
		select {
		case client.Send <- broadcastPayload:
		default:
			// Don't unregister here, just log. The main pumps will handle unresponsive clients.
			log.Printf("Client %s's send buffer was full during presence update.", client.UserID)
		}
	}
}
