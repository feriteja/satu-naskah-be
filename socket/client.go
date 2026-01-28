package socket

import (
	"encoding/json"
	"log"
	"net/http"
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
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	docID := r.URL.Query().Get("docId")
	if docID == "" {
		log.Println("Missing docId")
		return
	}

	client := &Client{
		Hub:    hub,
		Conn:   conn,
		DocID:  docID,
		UserID: userID,
		Send:   make(chan []byte, 256),
	}

	client.Hub.Register <- client

	// Start reading and writing in separate goroutines
	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.Hub.Unregister <- c
		c.Conn.Close()
	}()

	for {
		_, rawMessage, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}

		// Unmarshal the message so the hub can inspect its type.
		var msg WSMessage
		if err := json.Unmarshal(rawMessage, &msg); err != nil {
			log.Printf("Error unmarshalling message: %v", err)
			continue
		}

		// Set server-authoritative fields to prevent spoofing.
		msg.DocID = c.DocID
		msg.UserID = c.UserID

		// Send the parsed message to the hub.
		c.Hub.Broadcast <- msg
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second) // Send ping every 30s
	defer ticker.Stop()

	for {
		select {
		case message := <-c.Send:
			c.Conn.WriteMessage(websocket.TextMessage, message)
		case <-ticker.C:
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return // Connection is dead
			}
		}
	}
}
