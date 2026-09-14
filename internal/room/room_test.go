package room

import (
	"testing"

	"github.com/kelvinzer0/llm-bridge-go/internal/protocol"
)

func TestRoomModelsRegistration(t *testing.T) {
	r := New("test-room", "secret_token")

	if r.IsConnected() {
		t.Error("expected room to not be connected initially")
	}

	models := []protocol.ModelDefinition{
		{
			ID:      "gpt-4o",
			Name:    "GPT-4o",
			OwnedBy: "openai",
		},
		{
			ID:      "anthropic/claude-3-5-sonnet",
			Name:    "Claude 3.5 Sonnet",
			OwnedBy: "anthropic",
		},
	}

	r.RegisterModels(models)

	list := r.GetModels()
	if len(list) != 2 {
		t.Errorf("expected 2 models, got %d", len(list))
	}

	// Test model resolver (case insensitive and slash prefix)
	resolved, ok := r.ResolveModelID("gpt-4o")
	if !ok || resolved != "gpt-4o" {
		t.Errorf("failed to resolve exact model: %s", resolved)
	}

	resolved, ok = r.ResolveModelID("claude-3-5-sonnet")
	if !ok {
		t.Error("failed to resolve model without provider prefix")
	}

	// Test unregister
	r.UnregisterModels([]string{"gpt-4o"})
	if _, ok := r.ResolveModelID("gpt-4o"); ok {
		t.Error("expected gpt-4o to be removed")
	}
}

func TestHubAuthValidation(t *testing.T) {
	hub := NewHub()

	hub.Create("room-x", "token123")

	// Valid auth
	room1, ok := hub.ValidateAuth("token123", "room-x")
	if !ok || room1 == nil {
		t.Fatal("expected valid auth to succeed")
	}

	// Invalid auth
	_, ok = hub.ValidateAuth("wrong_token", "room-x")
	if ok {
		t.Error("expected invalid token to fail")
	}

	// Non-existent room auto-creation
	room2, ok := hub.ValidateAuth("token_new", "room-new")
	if !ok || room2 == nil {
		t.Fatal("expected auto-creation for new room with token")
	}
}
