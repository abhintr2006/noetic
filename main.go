package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/cors"

	"cot-backend/internal/api"
	"cot-backend/internal/kafka"
	"cot-backend/internal/transformer"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// ── Context for graceful shutdown ──────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── Transformer model ──────────────────────────────────────────────────────
	cfg := transformer.DefaultConfig()
	model := transformer.NewModel(cfg)
	pipeline := transformer.NewPipeline(model)

	// ── Kafka service ──────────────────────────────────────────────────────────
	// Reads KAFKA_BROKERS from env (e.g. "localhost:9092" or "b1:9092,b2:9092").
	// If the env var is empty the Kafka service runs in no-op / disabled mode.
	kafkaSvc := kafka.NewService(os.Getenv("KAFKA_BROKERS"))
	defer kafkaSvc.Close()

	// Start async consumer: listens on "reasoning-requests" topic, calls pipeline,
	// and publishes results back to "reasoning-traces".
	kafkaSvc.StartRequestConsumer(ctx, pipeline)

	// ── HTTP router ────────────────────────────────────────────────────────────
	router := api.NewRouter(model, kafkaSvc)

	handler := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type"},
	}).Handler(router)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 90 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// ── Start server ───────────────────────────────────────────────────────────
	go func() {
		log.Printf("CoT Visualization backend listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// ── Graceful shutdown on SIGINT / SIGTERM ──────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutdown signal received — draining connections…")
	cancel() // stop Kafka consumer

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}
	log.Println("server stopped cleanly")
}
