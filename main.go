package main

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"satunaskah/middleware"
	"satunaskah/socket"
	"strings"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq" // Example for PostgreSQL
)

func main() {
	// Load .env file
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, using environment variables from OS")
	}

	// Database connection setup (ensure you have a valid password here)
	// Replace with your actual connection string
	dbUser := strings.TrimSpace(os.Getenv("user"))
	dbPass := strings.TrimSpace(os.Getenv("password"))
	dbHost := strings.TrimSpace(os.Getenv("host"))
	dbPort := strings.TrimSpace(os.Getenv("port"))
	dbName := strings.TrimSpace(os.Getenv("dbname"))

	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=require", dbUser, dbPass, dbHost, dbPort, dbName)

	// Debug: Check if the host exists before connecting
	if u, err := url.Parse(connStr); err == nil {
		host := u.Hostname()
		log.Printf("Resolving host: %s", host)
		if ips, err := net.LookupHost(host); err != nil {
			log.Printf("CRITICAL ERROR: DNS Lookup failed. Host %s does not exist. Check your DATABASE_URL or Supabase status.", host)
		} else {
			log.Printf("Host found. IPs: %v", ips)
		}
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Failed to open database connection: %v", err)
	}
	defer db.Close()

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

	hub := socket.NewHub(db)
	go hub.Run()
	go hub.SaveWorker() // Start the background worker for saving documents

	// Wrap the WebSocket endpoint with Auth
	wsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract UserID from context (set by middleware)
		userID := r.Context().Value(middleware.UserIDKey).(string)

		// Pass userID into your ServeWs function
		socket.ServeWs(hub, w, r, userID)
	})

	http.Handle("/ws", middleware.AuthMiddleware(wsHandler))

	log.Println("Go Backend listening on :8080")
	http.ListenAndServe(":8080", nil)
}
