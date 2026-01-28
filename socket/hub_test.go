package socket

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to read messages from a WebSocket connection with a timeout.
func readMessage(t *testing.T, conn *websocket.Conn) WSMessage {
	var msg WSMessage
	// Set a deadline to avoid tests hanging forever.
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, p, err := conn.ReadMessage()
	require.NoError(t, err, "Failed to read message from WebSocket")
	err = json.Unmarshal(p, &msg)
	require.NoError(t, err, "Failed to unmarshal WSMessage JSON")
	return msg
}

func TestHubIntegration(t *testing.T) {
	// 1. Setup Mock DB and Hub
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	hub := NewHub(db)
	go hub.Run()

	// 2. Setup Test HTTP Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// For simplicity, we'll hardcode the user ID for tests.
		userID := r.URL.Query().Get("user_id")
		ServeWs(hub, w, r, userID)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// --- Test Scenario ---

	// 3. Client 1 Joins
	docID := "test-doc-1"
	initialContent := `{"ops":[{"insert":"Hello World"}]}`

	// Expect a DB query when the first user joins a room.
	mock.ExpectQuery("SELECT content FROM documents WHERE id = \\$1").
		WithArgs(docID).
		WillReturnRows(sqlmock.NewRows([]string{"content"}).AddRow([]byte(initialContent)))

	// Connect client 1
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL+"/ws?docId="+docID+"&user_id=user1", nil)
	require.NoError(t, err, "Client 1 failed to connect")
	defer conn1.Close()

	// Client 1 should immediately receive the full document content.
	initialMsg := readMessage(t, conn1)
	assert.Equal(t, UpdateType, initialMsg.Type)
	assert.Equal(t, docID, initialMsg.DocID)
	assert.JSONEq(t, initialContent, string(initialMsg.Payload))

	// 4. Client 2 Joins the same room
	conn2, _, err := websocket.DefaultDialer.Dial(wsURL+"/ws?docId="+docID+"&user_id=user2", nil)
	require.NoError(t, err, "Client 2 failed to connect")
	defer conn2.Close()

	// Client 2 receives its own initial content message.
	_ = readMessage(t, conn2)

	// Client 1 should receive a presence update about Client 2 joining.
	presenceUpdateMsg := readMessage(t, conn1)
	assert.Equal(t, "PRESENCE_UPDATE", presenceUpdateMsg.Type)
	var statuses []UserStatus
	err = json.Unmarshal(presenceUpdateMsg.Payload, &statuses)
	require.NoError(t, err)
	assert.Len(t, statuses, 2, "Should be two users in the room")
	userIDs := []string{statuses[0].UserID, statuses[1].UserID}
	assert.Contains(t, userIDs, "user1")
	assert.Contains(t, userIDs, "user2")

	// 5. Client 2 sends a document update
	updatePayload := `{"ops":[{"retain":11},{"insert":"!"}]}`
	msgToSend := WSMessage{
		Type:    UpdateType,
		Payload: json.RawMessage(updatePayload),
	}
	msgBytes, _ := json.Marshal(msgToSend)
	err = conn2.WriteMessage(websocket.TextMessage, msgBytes)
	require.NoError(t, err, "Client 2 failed to send update message")

	// Client 1 should receive the broadcasted update from Client 2.
	broadcastMsg := readMessage(t, conn1)
	assert.Equal(t, UpdateType, broadcastMsg.Type)
	assert.Equal(t, "user2", broadcastMsg.UserID, "Broadcast message should have correct UserID")
	assert.JSONEq(t, updatePayload, string(broadcastMsg.Payload))

	// Ensure all mock expectations were met.
	assert.NoError(t, mock.ExpectationsWereMet())
}
