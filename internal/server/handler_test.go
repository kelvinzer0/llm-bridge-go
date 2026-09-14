package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kelvinzer0/llm-bridge-go/internal/protocol"
	"github.com/kelvinzer0/llm-bridge-go/internal/room"
)

func TestHandleNew(t *testing.T) {
	hub := room.NewHub()
	handler := NewHandler(hub)

	req := httptest.NewRequest(http.MethodGet, "/new", nil)
	w := httptest.NewRecorder()

	handler.HandleNew(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["room"] == "" || resp["api_key"] == "" {
		t.Error("expected room and api_key to be generated")
	}
}

func TestHandleHealth(t *testing.T) {
	hub := room.NewHub()
	handler := NewHandler(hub)

	r := hub.GetOrCreate("my-room", "my-token")
	r.RegisterModels([]protocol.ModelDefinition{{ID: "model-1", OwnedBy: "test"}})

	req := httptest.NewRequest(http.MethodGet, "/health?room=my-room", nil)
	w := httptest.NewRecorder()

	handler.HandleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)

	if resp["modelsRegistered"] != float64(1) {
		t.Errorf("expected 1 model registered, got %v", resp["modelsRegistered"])
	}
}

func TestAuthValidation(t *testing.T) {
	hub := room.NewHub()
	handler := NewHandler(hub)

	hub.Create("room1", "secretkey")

	// Missing auth header
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	handler.HandleModels(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing auth, got %d", w.Code)
	}

	// Invalid auth header format
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer invalidtokenwithoutunderscore")
	w = httptest.NewRecorder()
	handler.HandleModels(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad token format, got %d", w.Code)
	}

	// Correct auth header
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer secretkey_room1")
	w = httptest.NewRecorder()
	handler.HandleModels(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for valid token, got %d", w.Code)
	}
}

func TestCompletionsValidation(t *testing.T) {
	hub := room.NewHub()
	handler := NewHandler(hub)

	hub.Create("room1", "secret")

	// Empty body
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer secret_room1")
	w := httptest.NewRecorder()

	handler.HandleChatCompletions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty completion body, got %d", w.Code)
	}
}
