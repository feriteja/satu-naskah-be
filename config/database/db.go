package database

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"satunaskah/pkg/logger"

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
		logger.Sugar.Fatalf("Failed to open database connection: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err = db.Ping(); err == nil {
			logger.Sugar.Info("Successfully connected to the database")
			return db
		}
		logger.Sugar.Infof("Database connection failed, retrying in 2s... (%v)", err)
		time.Sleep(2 * time.Second)
	}
	logger.Sugar.Fatal("Could not connect to database after retries. Check your internet or Supabase status.")
	return nil
}
