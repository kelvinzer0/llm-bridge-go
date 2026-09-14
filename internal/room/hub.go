package room

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

func (h *Hub) Create(id, token string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()

	r := New(id, token)
	h.rooms[id] = r
	return r
}

func (h *Hub) Get(id string) *Room {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.rooms[id]
}

func (h *Hub) GetOrCreate(id, token string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()

	if r, exists := h.rooms[id]; exists {
		if r.Token == "" && token != "" {
			r.Token = token
		}
		return r
	}

	r := New(id, token)
	h.rooms[id] = r
	return r
}

func (h *Hub) ValidateAuth(token, roomID string) (*Room, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	r, exists := h.rooms[roomID]
	if !exists {
		r = New(roomID, token)
		h.rooms[roomID] = r
		return r, true
	}

	if r.Token == "" || r.Token == token {
		return r, true
	}

	return nil, false
}
