package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	handlers "satunaskah/handler"
	"satunaskah/middleware"
	"satunaskah/socket"
	"strings"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	// 1. The application starts here. First, it loads environment variables from a .env file.
	// Load .env file
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, using environment variables from OS")
	}

	dbUser := strings.TrimSpace(os.Getenv("user"))
	dbPass := strings.TrimSpace(os.Getenv("password"))
	dbHost := strings.TrimSpace(os.Getenv("host"))
	dbPort := strings.TrimSpace(os.Getenv("port"))
	dbName := strings.TrimSpace(os.Getenv("dbname"))

	// 2. It constructs the database connection string and attempts to connect to the PostgreSQL database.
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=require", dbUser, dbPass, dbHost, dbPort, dbName)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Failed to open database connection: %v", err)
	}
	defer db.Close()

	// 3. It pings the database with a retry mechanism to ensure the connection is stable before proceeding.
	// Verify the connection is actually alive
	// Retry a few times in case of temporary DNS/network blips
	for i := 0; i < 5; i++ {
		if err = db.Ping(); err == nil {
			break
		}
		log.Printf("Database connection failed, retrying in 2s... (%v)", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Fatalf("Could not connect to database after retries. Check your internet or Supabase status.")
	}
	log.Println("Successfully connected to the database")

	// 4. A new Hub is created. The Hub is the central component that manages all clients and rooms.
	hub := socket.NewHub(db)
	// The Hub's main event loop is started in a separate goroutine so it doesn't block the main thread.
	go hub.Run()
	// The SaveWorker is also started in a goroutine to handle periodic database saves.
	go hub.SaveWorker()

	// 5. An HTTP handler is defined for the "/ws" endpoint. This is where WebSocket connection requests will come.
	// Wrap the WebSocket endpoint with Auth
	wsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 8. After authentication (see step 7 in middleware/auth.go), the user's ID is retrieved from the request context.
		userID := r.Context().Value(middleware.UserIDKey).(string)
		// The request is handed off to ServeWs to upgrade the connection.
		socket.ServeWs(hub, w, r, userID)
	})

	// 6. The "/ws" handler is wrapped with our authentication middleware. This means every connection attempt to "/ws" must first pass the authentication check.
	http.Handle("/ws", middleware.AuthMiddleware(wsHandler))

	// --- REST API Routes ---
	docHandler := &handlers.DocumentHandler{DB: db, Hub: hub}

	http.Handle("/api/documents/create", middleware.AuthMiddleware(http.HandlerFunc(docHandler.CreateDocument)))
	http.Handle("/api/documents/delete", middleware.AuthMiddleware(http.HandlerFunc(docHandler.DeleteDocument)))
	http.Handle("/api/documents/update", middleware.AuthMiddleware(http.HandlerFunc(docHandler.UpdateDocument)))
	http.Handle("/api/documents", middleware.AuthMiddleware(http.HandlerFunc(docHandler.GetDocuments)))
	http.Handle("/api/documents/invite", middleware.AuthMiddleware(http.HandlerFunc(docHandler.AddCollaborator)))
	http.Handle("/api/documents/comments/add", middleware.AuthMiddleware(http.HandlerFunc(docHandler.AddComment)))
	http.Handle("/api/documents/comments", middleware.AuthMiddleware(http.HandlerFunc(docHandler.GetComments)))
	http.Handle("/api/documents/members", middleware.AuthMiddleware(http.HandlerFunc(docHandler.GetDocumentMembers)))
	http.Handle("/api/documents/save", middleware.AuthMiddleware(http.HandlerFunc(docHandler.SaveDocument)))

	log.Println("Go Backend listening on :8080")
	http.ListenAndServe(":8080", middleware.CORSMiddleware(http.DefaultServeMux))
}
