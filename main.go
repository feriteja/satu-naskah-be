package main

import (
	"net/http"
	"os"
	"satunaskah/config/database"
	"satunaskah/pkg/logger"
	"satunaskah/router"
	"satunaskah/socket"

	"github.com/joho/godotenv"
)

func main() {
	logger.Init()
	defer logger.Log.Sync()

	if err := godotenv.Load(); err != nil {
		logger.Sugar.Warn("No .env file found, using environment variables from OS")
	}

	db := database.Connect()
	defer db.Close()

	hub := socket.NewHub(db)
	go hub.Run()
	go hub.SaveWorker()

	mux := router.Setup(db, hub)

	logger.Log.Info("Go Backend listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		logger.Sugar.Errorw("Server failed", "error", err)
		os.Exit(1)
	}
}
