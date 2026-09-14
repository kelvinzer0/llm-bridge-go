package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	defaultPort := os.Getenv("PORT")
	if defaultPort == "" {
		defaultPort = "8080"
	}

	port := flag.String("port", defaultPort, "HTTP server port")
	host := flag.String("host", "0.0.0.0", "HTTP server host")
	flag.Parse()

	hub := NewHub()
	server := NewServer(hub)

	mux := http.NewServeMux()

	// Room & Extension routes
	mux.HandleFunc("/new", server.HandleNew)
	mux.HandleFunc("/health", server.HandleHealth)
	mux.HandleFunc("/ws/extension", server.HandleWSExtension)

	// OpenAI-compatible Chat Completions
	mux.HandleFunc("/v1/chat/completions", server.HandleChatCompletions)
	mux.HandleFunc("/chat/completions", server.HandleChatCompletions)

	// OpenAI-compatible Responses API
	mux.HandleFunc("/v1/responses", server.HandleResponses)
	mux.HandleFunc("/responses", server.HandleResponses)

	// OpenAI-compatible Embeddings
	mux.HandleFunc("/v1/embeddings", server.HandleEmbeddings)
	mux.HandleFunc("/embeddings", server.HandleEmbeddings)

	// OpenAI-compatible Models
	mux.HandleFunc("/v1/models", server.HandleModels)
	mux.HandleFunc("/v1/models/", server.HandleModels)
	mux.HandleFunc("/models", server.HandleModels)
	mux.HandleFunc("/models/", server.HandleModels)

	// Wrap with CORS
	handler := CorsMiddleware(mux)

	addr := fmt.Sprintf("%s:%s", *host, *port)
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 0, // 0 for streaming SSE / WebSocket connections
		IdleTimeout:  120 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("==================================================")
		log.Printf("  LLM Bridge (Go) — Universal OpenAI API Server")
		log.Printf("  Listening on: http://%s", addr)
		log.Printf("==================================================")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down server gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server stopped cleanly")
}
