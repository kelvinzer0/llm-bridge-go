package main

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type PendingCompletion struct {
	RequestID    string
	Model        string
	CompletionID string
	Created      int64
	Streaming    bool
	StreamChan   chan *ExtensionMessage
	ResultChan   chan *ExtensionMessage
	ErrChan      chan error
}

type PendingResponses struct {
	RequestID  string
	Model      string
	ResponseID string
	MessageID  string
	Created    int64
	Streaming  bool
	StreamChan chan *ExtensionMessage
	ResultChan chan *ExtensionMessage
	ErrChan    chan error
}

type PendingEmbedding struct {
	RequestID  string
	ResultChan chan *ExtensionMessage
	ErrChan    chan error
}

type Room struct {
	ID                 string
	Token              string
	mu                 sync.RWMutex
	models             map[string]ModelDefinition
	ws                 *websocket.Conn
	wsMu               sync.Mutex
	pendingCompletions map[string]*PendingCompletion
	pendingResponses   map[string]*PendingResponses
	pendingEmbeddings  map[string]*PendingEmbedding
}

func NewRoom(id, token string) *Room {
	return &Room{
		ID:                 id,
		Token:              token,
		models:             make(map[string]ModelDefinition),
		pendingCompletions: make(map[string]*PendingCompletion),
		pendingResponses:   make(map[string]*PendingResponses),
		pendingEmbeddings:  make(map[string]*PendingEmbedding),
	}
}

func (r *Room) SetWS(conn *websocket.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.ws != nil {
		_ = r.ws.Close()
	}
	r.ws = conn
}

func (r *Room) ClearWS(conn *websocket.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.ws == conn {
		r.ws = nil
		r.models = make(map[string]ModelDefinition)

		// Abort any pending completions
		for id, pc := range r.pendingCompletions {
			select {
			case pc.ErrChan <- errors.New("extension disconnected"):
			default:
			}
			delete(r.pendingCompletions, id)
		}

		for id, pr := range r.pendingResponses {
			select {
			case pr.ErrChan <- errors.New("extension disconnected"):
			default:
			}
			delete(r.pendingResponses, id)
		}

		for id, pe := range r.pendingEmbeddings {
			select {
			case pe.ErrChan <- errors.New("extension disconnected"):
			default:
			}
			delete(r.pendingEmbeddings, id)
		}
	}
}

func (r *Room) IsConnected() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ws != nil
}

func (r *Room) SendWSJSON(v interface{}) error {
	r.wsMu.Lock()
	defer r.wsMu.Unlock()

	r.mu.RLock()
	conn := r.ws
	r.mu.RUnlock()

	if conn == nil {
		return errors.New("extension not connected")
	}

	_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return conn.WriteJSON(v)
}

func (r *Room) RegisterModels(models []ModelDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().Unix()
	for _, m := range models {
		if m.Created == 0 {
			m.Created = now
		}
		r.models[m.ID] = m
	}
}

func (r *Room) UnregisterModels(ids []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		delete(r.models, id)
	}
}

func (r *Room) GetModels() []ModelDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]ModelDefinition, 0, len(r.models))
	for _, m := range r.models {
		list = append(list, m)
	}
	return list
}

func (r *Room) ResolveModelID(rawID string) (string, bool) {
	if rawID == "" {
		return "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	if _, exists := r.models[rawID]; exists {
		return rawID, true
	}

	cleanID := rawID
	if strings.Contains(rawID, "/") {
		parts := strings.Split(rawID, "/")
		cleanID = parts[len(parts)-1]
	}
	cleanID = strings.ToLower(strings.TrimSpace(cleanID))

	if _, exists := r.models[cleanID]; exists {
		return cleanID, true
	}

	rawLower := strings.ToLower(strings.TrimSpace(rawID))
	for id := range r.models {
		idLower := strings.ToLower(id)
		if idLower == cleanID || idLower == rawLower {
			return id, true
		}
	}

	return "", false
}

func (r *Room) GetModel(id string) (ModelDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[id]
	return m, ok
}

// ── Completion handlers ──

func (r *Room) AddPendingCompletion(pc *PendingCompletion) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingCompletions[pc.RequestID] = pc
}

func (r *Room) RemovePendingCompletion(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pendingCompletions, requestID)
}

func (r *Room) AddPendingResponses(pr *PendingResponses) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingResponses[pr.RequestID] = pr
}

func (r *Room) RemovePendingResponses(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pendingResponses, requestID)
}

func (r *Room) AddPendingEmbedding(pe *PendingEmbedding) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingEmbeddings[pe.RequestID] = pe
}

func (r *Room) RemovePendingEmbedding(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pendingEmbeddings, requestID)
}

// ── Dispatch incoming message to matching pending request ──

func (r *Room) DispatchExtensionMessage(msg *ExtensionMessage) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	reqID := msg.RequestID
	if reqID == "" {
		return
	}

	// 1. Check completions
	if pc, exists := r.pendingCompletions[reqID]; exists {
		switch msg.Type {
		case "stream":
			if pc.Streaming && pc.StreamChan != nil {
				select {
				case pc.StreamChan <- msg:
				default:
				}
			}
		case "response":
			if pc.Streaming && pc.StreamChan != nil {
				select {
				case pc.StreamChan <- msg:
				default:
				}
			}
			if !pc.Streaming && pc.ResultChan != nil {
				select {
				case pc.ResultChan <- msg:
				default:
				}
			}
		case "streamError":
			if pc.ErrChan != nil {
				select {
				case pc.ErrChan <- errors.New(msg.Error):
				default:
				}
			}
		}
		return
	}

	// 2. Check responses API
	if pr, exists := r.pendingResponses[reqID]; exists {
		switch msg.Type {
		case "stream":
			if pr.Streaming && pr.StreamChan != nil {
				select {
				case pr.StreamChan <- msg:
				default:
				}
			}
		case "response":
			if pr.Streaming && pr.StreamChan != nil {
				select {
				case pr.StreamChan <- msg:
				default:
				}
			}
			if !pr.Streaming && pr.ResultChan != nil {
				select {
				case pr.ResultChan <- msg:
				default:
				}
			}
		case "streamError":
			if pr.ErrChan != nil {
				select {
				case pr.ErrChan <- errors.New(msg.Error):
				default:
				}
			}
		}
		return
	}

	// 3. Check embeddings
	if pe, exists := r.pendingEmbeddings[reqID]; exists {
		switch msg.Type {
		case "embedResult":
			if pe.ResultChan != nil {
				select {
				case pe.ResultChan <- msg:
				default:
				}
			}
		case "streamError":
			if pe.ErrChan != nil {
				select {
				case pe.ErrChan <- errors.New(msg.Error):
				default:
				}
			}
		}
		return
	}
}
