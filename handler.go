package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  65536,
	WriteBufferSize: 65536,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Server struct {
	hub *Hub
}

func NewServer(hub *Hub) *Server {
	return &Server{hub: hub}
}

func (s *Server) getBaseURLs(r *http.Request) (httpBase, wsBase string) {
	proto := "http"
	wsProto := "ws"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		proto = "https"
		wsProto = "wss"
	}

	host := r.Host
	if fHost := r.Header.Get("X-Forwarded-Host"); fHost != "" {
		host = fHost
	}

	return fmt.Sprintf("%s://%s", proto, host), fmt.Sprintf("%s://%s", wsProto, host)
}

func generateRandomID(length int) string {
	bytes := make([]byte, length/2+1)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())[:length]
	}
	return hex.EncodeToString(bytes)[:length]
}

func parseAuth(r *http.Request) (token, roomID string, ok bool) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", "", false
	}

	trimmed := strings.TrimSpace(auth)
	if !strings.HasPrefix(strings.ToLower(trimmed), "bearer ") {
		return "", "", false
	}

	bearer := strings.TrimSpace(trimmed[7:])
	lastUnderscore := strings.LastIndex(bearer, "_")
	if lastUnderscore <= 0 || lastUnderscore >= len(bearer)-1 {
		return "", "", false
	}

	return bearer[:lastUnderscore], bearer[lastUnderscore+1:], true
}

func sendOpenAIError(w http.ResponseWriter, message string, status int, errType, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	var codePtr *string
	if code != "" {
		codePtr = &code
	}

	resp := OpenAIErrorResponse{
		Error: OpenAIErrorDetail{
			Message: message,
			Type:    errType,
			Code:    codePtr,
			Param:   nil,
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ── Endpoints ───────────────────────────────────────────────────────

func (s *Server) HandleNew(w http.ResponseWriter, r *http.Request) {
	roomID := generateRandomID(8)
	token := generateRandomID(24)
	apiKey := fmt.Sprintf("%s_%s", token, roomID)

	_ = s.hub.CreateRoom(roomID, token)

	httpBase, wsBase := s.getBaseURLs(r)

	resp := map[string]interface{}{
		"room":          roomID,
		"extension_url": fmt.Sprintf("%s/ws/extension?room=%s", wsBase, roomID),
		"api_base_url":  fmt.Sprintf("%s/v1", httpBase),
		"api_key":       apiKey,
		"health_url":    fmt.Sprintf("%s/health?room=%s", httpBase, roomID),
		"usage": map[string]string{
			"curl":   fmt.Sprintf("curl %s/v1/chat/completions -H \"Authorization: Bearer %s\" -H \"Content-Type: application/json\" -d '{\"model\":\"MODEL_ID\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}'", httpBase, apiKey),
			"models": fmt.Sprintf("curl %s/v1/models -H \"Authorization: Bearer %s\"", httpBase, apiKey),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("room")
	if roomID == "" {
		roomID = "default"
	}

	room := s.hub.GetRoom(roomID)
	isConnected := false
	modelsRegistered := 0
	modelIDs := []string{}

	if room != nil {
		isConnected = room.IsConnected()
		models := room.GetModels()
		modelsRegistered = len(models)
		for _, m := range models {
			modelIDs = append(modelIDs, m.ID)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":             "ok",
		"extensionConnected": isConnected,
		"modelsRegistered":   modelsRegistered,
		"models":             modelIDs,
	})
}

func (s *Server) HandleWSExtension(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("room")
	if roomID == "" {
		roomID = "default"
	}

	room := s.hub.GetOrCreateRoom(roomID, "")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS] Upgrade failed: %v", err)
		return
	}

	room.SetWS(conn)
	defer func() {
		room.ClearWS(conn)
		_ = conn.Close()
	}()

	log.Printf("[WS] Extension connected to room '%s'", roomID)

	done := make(chan struct{})
	defer close(done)

	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := room.SendWSJSON(map[string]string{"type": "ping"}); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[WS] Unexpected close: %v", err)
			}
			break
		}

		var msg ExtensionMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "registerModels":
			if len(msg.Models) > 0 {
				room.RegisterModels(msg.Models)
				log.Printf("[Room %s] Registered %d models", roomID, len(msg.Models))
			}
		case "unregisterModels":
			if len(msg.IDs) > 0 {
				room.UnregisterModels(msg.IDs)
				log.Printf("[Room %s] Unregistered %d models", roomID, len(msg.IDs))
			}
		case "stream", "response", "streamError", "embedResult":
			room.DispatchExtensionMessage(&msg)
		case "pong":
			// heartbeat
		}
	}

	log.Printf("[WS] Extension disconnected from room '%s'", roomID)
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (*Room, bool) {
	token, roomID, ok := parseAuth(r)
	if !ok {
		sendOpenAIError(w, "Missing or invalid Authorization header. Expected: Bearer <token>_<roomId>", http.StatusUnauthorized, "invalid_request_error", "invalid_api_key")
		return nil, false
	}

	room, ok := s.hub.ValidateAuth(token, roomID)
	if !ok {
		sendOpenAIError(w, "Invalid token for the specified room", http.StatusUnauthorized, "invalid_request_error", "invalid_api_key")
		return nil, false
	}

	return room, true
}

// ── /v1/chat/completions ────────────────────────────────────────────

func (s *Server) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	room, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		sendOpenAIError(w, "Invalid request body", http.StatusBadRequest, "invalid_request_error", "")
		return
	}

	var req ChatCompletionRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		sendOpenAIError(w, "Invalid JSON body", http.StatusBadRequest, "invalid_request_error", "")
		return
	}

	if req.Model == "" {
		sendOpenAIError(w, "'model' is required", http.StatusBadRequest, "invalid_request_error", "")
		return
	}
	if len(req.Messages) == 0 {
		sendOpenAIError(w, "'messages' is required", http.StatusBadRequest, "invalid_request_error", "")
		return
	}

	resolvedModel, exists := room.ResolveModelID(req.Model)
	if !exists {
		sendOpenAIError(w, fmt.Sprintf("Model '%s' not found", req.Model), http.StatusNotFound, "invalid_request_error", "model_not_found")
		return
	}

	if !room.IsConnected() {
		sendOpenAIError(w, "Extension not connected", http.StatusServiceUnavailable, "server_error", "")
		return
	}

	requestID := uuid.NewString()
	completionID := fmt.Sprintf("chatcmpl-%s", uuid.NewString()[:12])
	created := time.Now().Unix()

	if req.Stream {
		s.handleStreamingCompletions(w, r, room, req, requestID, completionID, resolvedModel, created)
	} else {
		s.handleNonStreamingCompletions(w, r, room, req, requestID, completionID, resolvedModel, created)
	}
}

func (s *Server) handleStreamingCompletions(
	w http.ResponseWriter,
	r *http.Request,
	room *Room,
	req ChatCompletionRequest,
	requestID, completionID, resolvedModel string,
	created int64,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		sendOpenAIError(w, "Streaming unsupported", http.StatusInternalServerError, "server_error", "")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// 1. Send initial role chunk
	initChunk := ChatCompletionChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   resolvedModel,
		Choices: []ChatCompletionChunkChoice{
			{
				Index: 0,
				Delta: ChatCompletionChunkDelta{
					Role: "assistant",
				},
				FinishReason: nil,
			},
		},
	}
	initJSON, _ := json.Marshal(initChunk)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", initJSON)
	flusher.Flush()

	// 2. Register pending completion
	streamChan := make(chan *ExtensionMessage, 100)
	errChan := make(chan error, 1)

	pc := &PendingCompletion{
		RequestID:    requestID,
		Model:        resolvedModel,
		CompletionID: completionID,
		Created:      created,
		Streaming:    true,
		StreamChan:   streamChan,
		ErrChan:      errChan,
	}

	room.AddPendingCompletion(pc)
	defer room.RemovePendingCompletion(requestID)

	// 3. Send request to extension
	wsReq := CompletionRequestMessage{
		Type:      "completionRequest",
		RequestID: requestID,
		Request:   req,
	}
	if err := room.SendWSJSON(wsReq); err != nil {
		sendStreamError(w, flusher, err.Error())
		return
	}

	// 4. Stream loop
	timeout := time.After(300 * time.Second)

	for {
		select {
		case <-r.Context().Done():
			return
		case <-timeout:
			sendStreamError(w, flusher, "Request timeout")
			return
		case err := <-errChan:
			sendStreamError(w, flusher, err.Error())
			return
		case msg := <-streamChan:
			if msg == nil {
				return
			}
			if msg.Type == "stream" && msg.Delta != nil {
				content := msg.Delta.Content
				chunk := ChatCompletionChunk{
					ID:      completionID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   resolvedModel,
					Choices: []ChatCompletionChunkChoice{
						{
							Index: 0,
							Delta: ChatCompletionChunkDelta{
								Content: &content,
							},
							FinishReason: nil,
						},
					},
				}
				chunkJSON, _ := json.Marshal(chunk)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", chunkJSON)
				flusher.Flush()
			} else if msg.Type == "response" {
				finishReason := "stop"
				if msg.FinishReason != nil && *msg.FinishReason != "" {
					finishReason = *msg.FinishReason
				} else if len(msg.ToolCalls) > 0 {
					finishReason = "tool_calls"
				}

				finalChunk := ChatCompletionChunk{
					ID:      completionID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   resolvedModel,
					Choices: []ChatCompletionChunkChoice{
						{
							Index: 0,
							Delta: ChatCompletionChunkDelta{
								ToolCalls: msg.ToolCalls,
							},
							FinishReason: &finishReason,
						},
					},
				}
				finalJSON, _ := json.Marshal(finalChunk)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", finalJSON)
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
		}
	}
}

func sendStreamError(w http.ResponseWriter, flusher http.Flusher, errMessage string) {
	errResp := OpenAIErrorResponse{
		Error: OpenAIErrorDetail{
			Message: errMessage,
			Type:    "server_error",
			Code:    nil,
			Param:   nil,
		},
	}
	errJSON, _ := json.Marshal(errResp)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", errJSON)
	_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (s *Server) handleNonStreamingCompletions(
	w http.ResponseWriter,
	r *http.Request,
	room *Room,
	req ChatCompletionRequest,
	requestID, completionID, resolvedModel string,
	created int64,
) {
	resultChan := make(chan *ExtensionMessage, 1)
	errChan := make(chan error, 1)

	pc := &PendingCompletion{
		RequestID:    requestID,
		Model:        resolvedModel,
		CompletionID: completionID,
		Created:      created,
		Streaming:    false,
		ResultChan:   resultChan,
		ErrChan:      errChan,
	}

	room.AddPendingCompletion(pc)
	defer room.RemovePendingCompletion(requestID)

	wsReq := CompletionRequestMessage{
		Type:      "completionRequest",
		RequestID: requestID,
		Request:   req,
	}
	if err := room.SendWSJSON(wsReq); err != nil {
		sendOpenAIError(w, err.Error(), http.StatusInternalServerError, "server_error", "")
		return
	}

	select {
	case <-r.Context().Done():
		return
	case <-time.After(300 * time.Second):
		sendOpenAIError(w, "Request timeout", http.StatusGatewayTimeout, "server_error", "")
		return
	case err := <-errChan:
		sendOpenAIError(w, err.Error(), http.StatusInternalServerError, "server_error", "")
		return
	case msg := <-resultChan:
		finishReason := "stop"
		if msg.FinishReason != nil && *msg.FinishReason != "" {
			finishReason = *msg.FinishReason
		} else if len(msg.ToolCalls) > 0 {
			finishReason = "tool_calls"
		}

		var content *string
		if msg.Content != nil {
			content = msg.Content
		}

		usage := UsageInfo{
			PromptTokens:     0,
			CompletionTokens: 0,
			TotalTokens:      0,
		}
		if msg.Usage != nil {
			usage = *msg.Usage
		}

		resp := ChatCompletionResponse{
			ID:      completionID,
			Object:  "chat.completion",
			Created: created,
			Model:   resolvedModel,
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatCompletionChoiceMessage{
						Role:      "assistant",
						Content:   content,
						ToolCalls: msg.ToolCalls,
					},
					FinishReason: finishReason,
				},
			},
			Usage: usage,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// ── /v1/responses ───────────────────────────────────────────────────

func (s *Server) HandleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	room, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		sendOpenAIError(w, "Invalid request body", http.StatusBadRequest, "invalid_request_error", "")
		return
	}

	var req ResponsesAPIRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		sendOpenAIError(w, "Invalid JSON body", http.StatusBadRequest, "invalid_request_error", "")
		return
	}

	if req.Model == "" {
		sendOpenAIError(w, "'model' is required", http.StatusBadRequest, "invalid_request_error", "")
		return
	}
	if req.Input == nil {
		sendOpenAIError(w, "'input' is required", http.StatusBadRequest, "invalid_request_error", "")
		return
	}

	resolvedModel, exists := room.ResolveModelID(req.Model)
	if !exists {
		sendOpenAIError(w, fmt.Sprintf("Model '%s' not found", req.Model), http.StatusNotFound, "invalid_request_error", "model_not_found")
		return
	}

	if !room.IsConnected() {
		sendOpenAIError(w, "Extension not connected", http.StatusServiceUnavailable, "server_error", "")
		return
	}

	requestID := uuid.NewString()
	responseID := fmt.Sprintf("resp_%s", uuid.NewString()[:12])
	messageID := fmt.Sprintf("msg_%s", uuid.NewString()[:12])
	created := time.Now().Unix()

	if req.Stream {
		flusher, ok := w.(http.Flusher)
		if !ok {
			sendOpenAIError(w, "Streaming unsupported", http.StatusInternalServerError, "server_error", "")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		writeEvent := func(event string, data interface{}) {
			d, _ := json.Marshal(data)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, d)
			flusher.Flush()
		}

		initResp := ResponsesAPIResponse{
			ID:        responseID,
			Object:    "response",
			CreatedAt: created,
			Status:    "in_progress",
			Model:     resolvedModel,
			Output:    []ResponsesOutputItem{},
		}
		writeEvent("response.created", map[string]interface{}{"type": "response.created", "response": initResp})
		writeEvent("response.in_progress", map[string]interface{}{"type": "response.in_progress", "response": initResp})

		outItem := ResponsesOutputItem{
			Type:    "message",
			ID:      messageID,
			Role:    "assistant",
			Content: []ResponsesContentPart{},
		}
		writeEvent("response.output_item.added", map[string]interface{}{"type": "response.output_item.added", "output_index": 0, "item": outItem})
		writeEvent("response.content_part.added", map[string]interface{}{"type": "response.content_part.added", "output_index": 0, "content_index": 0, "part": ResponsesContentPart{Type: "output_text", Text: ""}})

		streamChan := make(chan *ExtensionMessage, 100)
		errChan := make(chan error, 1)

		pr := &PendingResponses{
			RequestID:  requestID,
			Model:      resolvedModel,
			ResponseID: responseID,
			MessageID:  messageID,
			Created:    created,
			Streaming:  true,
			StreamChan: streamChan,
			ErrChan:    errChan,
		}

		room.AddPendingResponses(pr)
		defer room.RemovePendingResponses(requestID)

		wsReq := ResponsesRequestMessage{
			Type:      "responsesRequest",
			RequestID: requestID,
			Request:   req,
		}
		_ = room.SendWSJSON(wsReq)

		var fullContent strings.Builder
		for {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(300 * time.Second):
				writeEvent("response.failed", map[string]interface{}{"type": "response.failed", "response": map[string]interface{}{"id": responseID, "status": "failed", "error": map[string]string{"message": "Timeout"}}})
				return
			case err := <-errChan:
				writeEvent("response.failed", map[string]interface{}{"type": "response.failed", "response": map[string]interface{}{"id": responseID, "status": "failed", "error": map[string]string{"message": err.Error()}}})
				return
			case msg := <-streamChan:
				if msg == nil {
					return
				}
				if msg.Type == "stream" && msg.Delta != nil {
					fullContent.WriteString(msg.Delta.Content)
					writeEvent("response.content_part.delta", map[string]interface{}{
						"type":          "response.content_part.delta",
						"output_index":  0,
						"content_index": 0,
						"delta":         map[string]string{"type": "text_delta", "text": msg.Delta.Content},
					})
				} else if msg.Type == "response" {
					text := fullContent.String()
					if msg.Content != nil && *msg.Content != "" {
						text = *msg.Content
					}

					writeEvent("response.content_part.done", map[string]interface{}{
						"type":          "response.content_part.done",
						"output_index":  0,
						"content_index": 0,
						"part":          map[string]string{"type": "output_text", "text": text},
					})
					writeEvent("response.output_item.done", map[string]interface{}{
						"type":         "response.output_item.done",
						"output_index": 0,
						"item": ResponsesOutputItem{
							Type:    "message",
							ID:      messageID,
							Role:    "assistant",
							Content: []ResponsesContentPart{{Type: "output_text", Text: text}},
						},
					})

					usage := &ResponsesUsage{InputTokens: 0, OutputTokens: 0, TotalTokens: 0}
					if msg.Usage != nil {
						usage.InputTokens = msg.Usage.PromptTokens
						usage.OutputTokens = msg.Usage.CompletionTokens
						usage.TotalTokens = msg.Usage.TotalTokens
					}

					writeEvent("response.completed", map[string]interface{}{
						"type": "response.completed",
						"response": ResponsesAPIResponse{
							ID:        responseID,
							Object:    "response",
							CreatedAt: created,
							Status:    "completed",
							Model:     resolvedModel,
							Output: []ResponsesOutputItem{
								{
									Type:    "message",
									ID:      messageID,
									Role:    "assistant",
									Content: []ResponsesContentPart{{Type: "output_text", Text: text}},
								},
							},
							Usage: usage,
						},
					})
					return
				}
			}
		}
	} else {
		resultChan := make(chan *ExtensionMessage, 1)
		errChan := make(chan error, 1)

		pr := &PendingResponses{
			RequestID:  requestID,
			Model:      resolvedModel,
			ResponseID: responseID,
			MessageID:  messageID,
			Created:    created,
			Streaming:  false,
			ResultChan: resultChan,
			ErrChan:    errChan,
		}

		room.AddPendingResponses(pr)
		defer room.RemovePendingResponses(requestID)

		wsReq := ResponsesRequestMessage{
			Type:      "responsesRequest",
			RequestID: requestID,
			Request:   req,
		}
		if err := room.SendWSJSON(wsReq); err != nil {
			sendOpenAIError(w, err.Error(), http.StatusInternalServerError, "server_error", "")
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-time.After(300 * time.Second):
			sendOpenAIError(w, "Request timeout", http.StatusGatewayTimeout, "server_error", "")
			return
		case err := <-errChan:
			sendOpenAIError(w, err.Error(), http.StatusInternalServerError, "server_error", "")
			return
		case msg := <-resultChan:
			text := ""
			if msg.Content != nil {
				text = *msg.Content
			}

			usage := &ResponsesUsage{InputTokens: 0, OutputTokens: 0, TotalTokens: 0}
			if msg.Usage != nil {
				usage.InputTokens = msg.Usage.PromptTokens
				usage.OutputTokens = msg.Usage.CompletionTokens
				usage.TotalTokens = msg.Usage.TotalTokens
			}

			resp := ResponsesAPIResponse{
				ID:        responseID,
				Object:    "response",
				CreatedAt: created,
				Status:    "completed",
				Model:     resolvedModel,
				Output: []ResponsesOutputItem{
					{
						Type:    "message",
						ID:      messageID,
						Role:    "assistant",
						Content: []ResponsesContentPart{{Type: "output_text", Text: text}},
					},
				},
				Usage: usage,
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}
	}
}

// ── /v1/embeddings ──────────────────────────────────────────────────

func (s *Server) HandleEmbeddings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	room, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		sendOpenAIError(w, "Invalid request body", http.StatusBadRequest, "invalid_request_error", "")
		return
	}

	var req EmbeddingRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		sendOpenAIError(w, "Invalid JSON body", http.StatusBadRequest, "invalid_request_error", "")
		return
	}

	if req.Model == "" {
		sendOpenAIError(w, "'model' is required", http.StatusBadRequest, "invalid_request_error", "")
		return
	}
	if req.Input == nil {
		sendOpenAIError(w, "'input' is required", http.StatusBadRequest, "invalid_request_error", "")
		return
	}

	resolvedModel, exists := room.ResolveModelID(req.Model)
	if !exists {
		sendOpenAIError(w, fmt.Sprintf("Model '%s' not found", req.Model), http.StatusNotFound, "invalid_request_error", "model_not_found")
		return
	}

	if !room.IsConnected() {
		sendOpenAIError(w, "Extension not connected", http.StatusServiceUnavailable, "server_error", "")
		return
	}

	requestID := uuid.NewString()
	resultChan := make(chan *ExtensionMessage, 1)
	errChan := make(chan error, 1)

	pe := &PendingEmbedding{
		RequestID:  requestID,
		ResultChan: resultChan,
		ErrChan:    errChan,
	}

	room.AddPendingEmbedding(pe)
	defer room.RemovePendingEmbedding(requestID)

	wsReq := EmbeddingRequestMessage{
		Type:      "embeddingRequest",
		RequestID: requestID,
		Request:   req,
	}
	if err := room.SendWSJSON(wsReq); err != nil {
		sendOpenAIError(w, err.Error(), http.StatusInternalServerError, "server_error", "")
		return
	}

	select {
	case <-r.Context().Done():
		return
	case <-time.After(60 * time.Second):
		sendOpenAIError(w, "Request timeout", http.StatusGatewayTimeout, "server_error", "")
		return
	case err := <-errChan:
		sendOpenAIError(w, err.Error(), http.StatusInternalServerError, "server_error", "")
		return
	case msg := <-resultChan:
		embData := make([]EmbeddingData, 0, len(msg.Embeddings))
		for i, emb := range msg.Embeddings {
			embData = append(embData, EmbeddingData{
				Object:    "embedding",
				Index:     i,
				Embedding: emb,
			})
		}

		usage := UsageInfo{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0}
		if msg.Usage != nil {
			usage = *msg.Usage
		}

		resp := EmbeddingResponse{
			Object: "list",
			Data:   embData,
			Model:  resolvedModel,
			Usage:  usage,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// ── /v1/models ──────────────────────────────────────────────────────

func (s *Server) HandleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	room, ok := s.authenticate(w, r)
	if !ok {
		return
	}

	// Check if specific model is requested: /v1/models/{id} or /models/{id}
	path := strings.TrimPrefix(r.URL.Path, "/v1/models")
	path = strings.TrimPrefix(path, "/models")
	path = strings.TrimPrefix(path, "/")

	if path != "" {
		model, found := room.GetModel(path)
		if !found {
			sendOpenAIError(w, fmt.Sprintf("Model '%s' not found", path), http.StatusNotFound, "invalid_request_error", "model_not_found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ModelObject{
			ID:      model.ID,
			Object:  "model",
			Created: model.Created,
			OwnedBy: model.OwnedBy,
		})
		return
	}

	models := room.GetModels()
	data := make([]ModelObject, 0, len(models))
	for _, m := range models {
		data = append(data, ModelObject{
			ID:      m.ID,
			Object:  "model",
			Created: m.Created,
			OwnedBy: m.OwnedBy,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ModelsListResponse{
		Object: "list",
		Data:   data,
	})
}

// ── CORS Middleware ─────────────────────────────────────────────────

func CorsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH, HEAD")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Expose-Headers", "*")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
