package main

import (
	"log"
	"net/http"
	"satunaskah/config/database"
	"satunaskah/router"
	"satunaskah/socket"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables from OS")
	}

	db := database.Connect()
	defer db.Close()

	hub := socket.NewHub(db)
	go hub.Run()
	go hub.SaveWorker()

	mux := router.Setup(db, hub)

	log.Println("Go Backend listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
