package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kelvinzer0/llm-bridge-go/internal/config"
	"github.com/kelvinzer0/llm-bridge-go/internal/room"
	"github.com/kelvinzer0/llm-bridge-go/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		log.Fatalf("Configuration error: %v", err)
	}

	hub := room.NewHub()
	handler := server.NewHandler(hub)

	mux := http.NewServeMux()

	// Room and extension routes
	mux.HandleFunc("/new", handler.HandleNew)
	mux.HandleFunc("/health", handler.HandleHealth)
	mux.HandleFunc("/ws/extension", handler.HandleWSExtension)

	// OpenAI-compatible Chat Completions
	mux.HandleFunc("/v1/chat/completions", handler.HandleChatCompletions)
	mux.HandleFunc("/chat/completions", handler.HandleChatCompletions)

	// OpenAI-compatible Responses API
	mux.HandleFunc("/v1/responses", handler.HandleResponses)
	mux.HandleFunc("/responses", handler.HandleResponses)

	// OpenAI-compatible Embeddings
	mux.HandleFunc("/v1/embeddings", handler.HandleEmbeddings)
	mux.HandleFunc("/embeddings", handler.HandleEmbeddings)

	// OpenAI-compatible Models
	mux.HandleFunc("/v1/models", handler.HandleModels)
	mux.HandleFunc("/v1/models/", handler.HandleModels)
	mux.HandleFunc("/models", handler.HandleModels)
	mux.HandleFunc("/models/", handler.HandleModels)

	corsHandler := server.CorsMiddleware(mux)

	httpServer := &http.Server{
		Addr:         cfg.Address(),
		Handler:      corsHandler,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Starting llm-bridge server on http://%s", cfg.Address())
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down llm-bridge server gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server stopped cleanly")
}
