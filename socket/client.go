package socket

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"satunaskah/pkg/logger"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// CheckOrigin allows us to connect from our Next.js dev server
	CheckOrigin: func(r *http.Request) bool { return true },
}

func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request, userID string) {
	// 9. The HTTP connection is upgraded to a persistent WebSocket connection.
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Sugar.Error(err)
		return
	}

	docID := r.URL.Query().Get("docId")
	if docID == "" {
		logger.Sugar.Error("Missing docId")
		return
	}

	// --- Determine User Role ---
	// Default to Reader
	role := RoleReader

	// 1. Check if Owner (Implicit Writer)
	var ownerID string
	var title string
	err = hub.db.QueryRow("SELECT owner_id, title FROM documents WHERE id = $1", docID).Scan(&ownerID, &title)
	if err == sql.ErrNoRows {
		logger.Sugar.Warnf("Connection rejected: Document %s not found", docID)
		return // Close connection if document doesn't exist
	} else if err != nil {
		logger.Sugar.Errorf("Database error checking owner: %v", err)
		return
	}

	if ownerID == userID {
		role = RoleWriter
	} else {
		// 2. Check Collaborators Table (You need to create this table in your DB)
		var dbRole string
		if err := hub.db.QueryRow("SELECT role FROM collaborators WHERE document_id = $1 AND user_id = $2", docID, userID).Scan(&dbRole); err == nil {
			role = dbRole
		}
	}

	// 10. A `Client` struct is created to represent this user's connection.
	// It holds references to the Hub, the connection itself, and the user/document IDs.
	client := &Client{
		Hub:    hub,
		Conn:   conn,
		DocID:  docID,
		UserID: userID,
		Role:   role,
		Title:  title,
		Send:   make(chan []byte, 256),
	}

	// 11. The newly created client is sent to the Hub's `Register` channel to be formally added to a room.
	client.Hub.Register <- client

	// 12. Two goroutines are started for this client. They run concurrently and handle reading and writing messages.
	// This is a standard and efficient pattern for WebSockets in Go.
	// Start reading and writing in separate goroutines
	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	// This function runs in a loop, constantly waiting for new messages from the client's browser.
	defer func() {
		// 18. If the loop breaks (e.g., the user closes their tab), the client is sent to the `Unregister` channel,
		//  and the connection is closed.
		c.Hub.Unregister <- c
		c.Conn.Close()
	}()

	for {
		// 15. A user performs an action (like typing), and their browser sends a message.
		//  This line reads that message from the WebSocket.
		_, rawMessage, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Sugar.Errorf("error: %v", err)
			}
			break
		}

		// Unmarshal the message so the hub can inspect its type.
		var msg WSMessage
		if err := json.Unmarshal(rawMessage, &msg); err != nil {
			logger.Sugar.Errorf("Error unmarshalling message: %v", err)
			continue
		}

		// To prevent a malicious user from sending messages on behalf of others,
		//  we overwrite the UserID and DocID with the server-authoritative values from the client struct.
		// Set server-authoritative fields to prevent spoofing.
		msg.DocID = c.DocID
		msg.UserID = c.UserID

		// --- RBAC: Enforce Permissions ---
		switch msg.Type {
		case UpdateType:
			// Only Writers can edit text
			if c.Role != RoleWriter {
				logger.Sugar.Warnf("Permission Denied: User %s (Role: %s) tried to edit doc %s", c.UserID, c.Role, c.DocID)
				continue
			}
		}

		// 16. The validated message is sent to the Hub's `Broadcast` channel for processing and distribution to other clients.
		// Send the parsed message to the hub.
		c.Hub.Broadcast <- msg
	}
}

func (c *Client) writePump() {
	// This function runs in a loop, waiting for messages that need to be sent *to* the client's browser.
	ticker := time.NewTicker(30 * time.Second) // Send ping every 30s
	defer ticker.Stop()

	for {
		select {
		case message := <-c.Send:
			c.Conn.WriteMessage(websocket.TextMessage, message)
		// A ticker sends a 'ping' message every 30 seconds to keep the connection alive and detect if it has dropped.
		case <-ticker.C:
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return // Connection is dead
			}
		}
	}
}
