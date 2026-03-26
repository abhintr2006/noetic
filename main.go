package main

import (
	"log"
	"net/http"
	"os"

	"github.com/rs/cors"

	"cot-backend/internal/api"
	"cot-backend/internal/transformer"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialize transformer model
	cfg := transformer.DefaultConfig()
	model := transformer.NewModel(cfg)

	// Wire up routes
	router := api.NewRouter(model)

	// CORS for frontend dev
	handler := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type"},
	}).Handler(router)

	log.Printf("CoT Visualization backend listening on :%s", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatal(err)
	}
}
