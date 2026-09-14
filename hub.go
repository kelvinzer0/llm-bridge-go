package main

import (
	"sync"
)

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]*Room
}

func NewHub() *Hub {
	return &Hub{
		rooms: make(map[string]*Room),
	}
}

func (h *Hub) CreateRoom(id, token string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()

	r := NewRoom(id, token)
	h.rooms[id] = r
	return r
}

func (h *Hub) GetRoom(id string) *Room {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.rooms[id]
}

func (h *Hub) GetOrCreateRoom(id, token string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()

	if r, exists := h.rooms[id]; exists {
		// Update token if was empty
		if r.Token == "" && token != "" {
			r.Token = token
		}
		return r
	}

	r := NewRoom(id, token)
	h.rooms[id] = r
	return r
}

func (h *Hub) ValidateAuth(token, roomID string) (*Room, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	r, exists := h.rooms[roomID]
	if !exists {
		// Auto-register room with provided token for self-host convenience
		r = NewRoom(roomID, token)
		h.rooms[roomID] = r
		return r, true
	}

	if r.Token == "" || r.Token == token {
		return r, true
	}

	return nil, false
}
