package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

func Connect() *sql.DB {
	dbUser := strings.TrimSpace(os.Getenv("user"))
	dbPass := strings.TrimSpace(os.Getenv("password"))
	dbHost := strings.TrimSpace(os.Getenv("host"))
	dbPort := strings.TrimSpace(os.Getenv("port"))
	dbName := strings.TrimSpace(os.Getenv("dbname"))

	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=require", dbUser, dbPass, dbHost, dbPort, dbName)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Failed to open database connection: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err = db.Ping(); err == nil {
			log.Println("Successfully connected to the database")
			return db
		}
		log.Printf("Database connection failed, retrying in 2s... (%v)", err)
		time.Sleep(2 * time.Second)
	}
	log.Fatalf("Could not connect to database after retries. Check your internet or Supabase status.")
	return nil
}
