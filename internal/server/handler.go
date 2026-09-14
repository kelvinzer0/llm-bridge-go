package server

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
	"github.com/kelvinzer0/llm-bridge-go/internal/protocol"
	"github.com/kelvinzer0/llm-bridge-go/internal/room"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  65536,
	WriteBufferSize: 65536,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Handler struct {
	hub *room.Hub
}

func NewHandler(hub *room.Hub) *Handler {
	return &Handler{hub: hub}
}

func (h *Handler) getBaseURLs(r *http.Request) (httpBase, wsBase string) {
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

	resp := protocol.OpenAIErrorResponse{
		Error: protocol.OpenAIErrorDetail{
			Message: message,
			Type:    errType,
			Code:    codePtr,
			Param:   nil,
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (*room.Room, bool) {
	token, roomID, ok := parseAuth(r)
	if !ok {
		sendOpenAIError(w, "Missing or invalid Authorization header. Expected: Bearer <token>_<roomId>", http.StatusUnauthorized, "invalid_request_error", "invalid_api_key")
		return nil, false
	}

	rInstance, ok := h.hub.ValidateAuth(token, roomID)
	if !ok {
		sendOpenAIError(w, "Invalid token for the specified room", http.StatusUnauthorized, "invalid_request_error", "invalid_api_key")
		return nil, false
	}

	return rInstance, true
}

// ── Room & Extension Routes ─────────────────────────────────────────

func (h *Handler) HandleNew(w http.ResponseWriter, r *http.Request) {
	roomID := generateRandomID(8)
	token := generateRandomID(24)
	apiKey := fmt.Sprintf("%s_%s", token, roomID)

	_ = h.hub.Create(roomID, token)

	httpBase, wsBase := h.getBaseURLs(r)

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

func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("room")
	if roomID == "" {
		roomID = "default"
	}

	rInstance := h.hub.Get(roomID)
	isConnected := false
	modelsRegistered := 0
	modelIDs := []string{}

	if rInstance != nil {
		isConnected = rInstance.IsConnected()
		models := rInstance.GetModels()
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

func (h *Handler) HandleWSExtension(w http.ResponseWriter, r *http.Request) {
	roomID := r.URL.Query().Get("room")
	if roomID == "" {
		roomID = "default"
	}

	rInstance := h.hub.GetOrCreate(roomID, "")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS] Upgrade failed: %v", err)
		return
	}

	rInstance.SetWS(conn)
	defer func() {
		rInstance.ClearWS(conn)
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
				if err := rInstance.SendWSJSON(map[string]string{"type": "ping"}); err != nil {
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

		var msg protocol.ExtensionMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "registerModels":
			if len(msg.Models) > 0 {
				rInstance.RegisterModels(msg.Models)
				log.Printf("[Room %s] Registered %d models", roomID, len(msg.Models))
			}
		case "unregisterModels":
			if len(msg.IDs) > 0 {
				rInstance.UnregisterModels(msg.IDs)
				log.Printf("[Room %s] Unregistered %d models", roomID, len(msg.IDs))
			}
		case "stream", "response", "streamError", "embedResult":
			rInstance.DispatchExtensionMessage(&msg)
		case "pong":
			// keepalive
		}
	}

	log.Printf("[WS] Extension disconnected from room '%s'", roomID)
}

// ── /v1/chat/completions ────────────────────────────────────────────

func (h *Handler) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	rInstance, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		sendOpenAIError(w, "Invalid request body", http.StatusBadRequest, "invalid_request_error", "")
		return
	}

	var req protocol.ChatCompletionRequest
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

	resolvedModel, exists := rInstance.ResolveModelID(req.Model)
	if !exists {
		sendOpenAIError(w, fmt.Sprintf("Model '%s' not found", req.Model), http.StatusNotFound, "invalid_request_error", "model_not_found")
		return
	}

	if !rInstance.IsConnected() {
		sendOpenAIError(w, "Extension not connected", http.StatusServiceUnavailable, "server_error", "")
		return
	}

	requestID := uuid.NewString()
	completionID := fmt.Sprintf("chatcmpl-%s", uuid.NewString()[:12])
	created := time.Now().Unix()

	if req.Stream {
		h.handleStreamingCompletions(w, r, rInstance, req, requestID, completionID, resolvedModel, created)
	} else {
		h.handleNonStreamingCompletions(w, r, rInstance, req, requestID, completionID, resolvedModel, created)
	}
}

func (h *Handler) handleStreamingCompletions(
	w http.ResponseWriter,
	r *http.Request,
	rInstance *room.Room,
	req protocol.ChatCompletionRequest,
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

	initChunk := protocol.ChatCompletionChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   resolvedModel,
		Choices: []protocol.ChatCompletionChunkChoice{
			{
				Index: 0,
				Delta: protocol.ChatCompletionChunkDelta{
					Role: "assistant",
				},
				FinishReason: nil,
			},
		},
	}
	initJSON, _ := json.Marshal(initChunk)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", initJSON)
	flusher.Flush()

	streamChan := make(chan *protocol.ExtensionMessage, 100)
	errChan := make(chan error, 1)

	pc := &room.PendingCompletion{
		RequestID:    requestID,
		Model:        resolvedModel,
		CompletionID: completionID,
		Created:      created,
		Streaming:    true,
		StreamChan:   streamChan,
		ErrChan:      errChan,
	}

	rInstance.AddPendingCompletion(pc)
	defer rInstance.RemovePendingCompletion(requestID)

	wsReq := protocol.CompletionRequestMessage{
		Type:      "completionRequest",
		RequestID: requestID,
		Request:   req,
	}
	if err := rInstance.SendWSJSON(wsReq); err != nil {
		sendStreamError(w, flusher, err.Error())
		return
	}

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
				chunk := protocol.ChatCompletionChunk{
					ID:      completionID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   resolvedModel,
					Choices: []protocol.ChatCompletionChunkChoice{
						{
							Index: 0,
							Delta: protocol.ChatCompletionChunkDelta{
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

				finalChunk := protocol.ChatCompletionChunk{
					ID:      completionID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   resolvedModel,
					Choices: []protocol.ChatCompletionChunkChoice{
						{
							Index: 0,
							Delta: protocol.ChatCompletionChunkDelta{
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
	errResp := protocol.OpenAIErrorResponse{
		Error: protocol.OpenAIErrorDetail{
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

func (h *Handler) handleNonStreamingCompletions(
	w http.ResponseWriter,
	r *http.Request,
	rInstance *room.Room,
	req protocol.ChatCompletionRequest,
	requestID, completionID, resolvedModel string,
	created int64,
) {
	resultChan := make(chan *protocol.ExtensionMessage, 1)
	errChan := make(chan error, 1)

	pc := &room.PendingCompletion{
		RequestID:    requestID,
		Model:        resolvedModel,
		CompletionID: completionID,
		Created:      created,
		Streaming:    false,
		ResultChan:   resultChan,
		ErrChan:      errChan,
	}

	rInstance.AddPendingCompletion(pc)
	defer rInstance.RemovePendingCompletion(requestID)

	wsReq := protocol.CompletionRequestMessage{
		Type:      "completionRequest",
		RequestID: requestID,
		Request:   req,
	}
	if err := rInstance.SendWSJSON(wsReq); err != nil {
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

		usage := protocol.UsageInfo{
			PromptTokens:     0,
			CompletionTokens: 0,
			TotalTokens:      0,
		}
		if msg.Usage != nil {
			usage = *msg.Usage
		}

		resp := protocol.ChatCompletionResponse{
			ID:      completionID,
			Object:  "chat.completion",
			Created: created,
			Model:   resolvedModel,
			Choices: []protocol.ChatCompletionChoice{
				{
					Index: 0,
					Message: protocol.ChatCompletionChoiceMessage{
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

func (h *Handler) HandleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	rInstance, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		sendOpenAIError(w, "Invalid request body", http.StatusBadRequest, "invalid_request_error", "")
		return
	}

	var req protocol.ResponsesAPIRequest
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

	resolvedModel, exists := rInstance.ResolveModelID(req.Model)
	if !exists {
		sendOpenAIError(w, fmt.Sprintf("Model '%s' not found", req.Model), http.StatusNotFound, "invalid_request_error", "model_not_found")
		return
	}

	if !rInstance.IsConnected() {
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

		initResp := protocol.ResponsesAPIResponse{
			ID:        responseID,
			Object:    "response",
			CreatedAt: created,
			Status:    "in_progress",
			Model:     resolvedModel,
			Output:    []protocol.ResponsesOutputItem{},
		}
		writeEvent("response.created", map[string]interface{}{"type": "response.created", "response": initResp})
		writeEvent("response.in_progress", map[string]interface{}{"type": "response.in_progress", "response": initResp})

		outItem := protocol.ResponsesOutputItem{
			Type:    "message",
			ID:      messageID,
			Role:    "assistant",
			Content: []protocol.ResponsesContentPart{},
		}
		writeEvent("response.output_item.added", map[string]interface{}{"type": "response.output_item.added", "output_index": 0, "item": outItem})
		writeEvent("response.content_part.added", map[string]interface{}{"type": "response.content_part.added", "output_index": 0, "content_index": 0, "part": protocol.ResponsesContentPart{Type: "output_text", Text: ""}})

		streamChan := make(chan *protocol.ExtensionMessage, 100)
		errChan := make(chan error, 1)

		pr := &room.PendingResponses{
			RequestID:  requestID,
			Model:      resolvedModel,
			ResponseID: responseID,
			MessageID:  messageID,
			Created:    created,
			Streaming:  true,
			StreamChan: streamChan,
			ErrChan:    errChan,
		}

		rInstance.AddPendingResponses(pr)
		defer rInstance.RemovePendingResponses(requestID)

		wsReq := protocol.ResponsesRequestMessage{
			Type:      "responsesRequest",
			RequestID: requestID,
			Request:   req,
		}
		_ = rInstance.SendWSJSON(wsReq)

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
						"item": protocol.ResponsesOutputItem{
							Type:    "message",
							ID:      messageID,
							Role:    "assistant",
							Content: []protocol.ResponsesContentPart{{Type: "output_text", Text: text}},
						},
					})

					usage := &protocol.ResponsesUsage{InputTokens: 0, OutputTokens: 0, TotalTokens: 0}
					if msg.Usage != nil {
						usage.InputTokens = msg.Usage.PromptTokens
						usage.OutputTokens = msg.Usage.CompletionTokens
						usage.TotalTokens = msg.Usage.TotalTokens
					}

					writeEvent("response.completed", map[string]interface{}{
						"type": "response.completed",
						"response": protocol.ResponsesAPIResponse{
							ID:        responseID,
							Object:    "response",
							CreatedAt: created,
							Status:    "completed",
							Model:     resolvedModel,
							Output: []protocol.ResponsesOutputItem{
								{
									Type:    "message",
									ID:      messageID,
									Role:    "assistant",
									Content: []protocol.ResponsesContentPart{{Type: "output_text", Text: text}},
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
		resultChan := make(chan *protocol.ExtensionMessage, 1)
		errChan := make(chan error, 1)

		pr := &room.PendingResponses{
			RequestID:  requestID,
			Model:      resolvedModel,
			ResponseID: responseID,
			MessageID:  messageID,
			Created:    created,
			Streaming:  false,
			ResultChan: resultChan,
			ErrChan:    errChan,
		}

		rInstance.AddPendingResponses(pr)
		defer rInstance.RemovePendingResponses(requestID)

		wsReq := protocol.ResponsesRequestMessage{
			Type:      "responsesRequest",
			RequestID: requestID,
			Request:   req,
		}
		if err := rInstance.SendWSJSON(wsReq); err != nil {
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

			usage := &protocol.ResponsesUsage{InputTokens: 0, OutputTokens: 0, TotalTokens: 0}
			if msg.Usage != nil {
				usage.InputTokens = msg.Usage.PromptTokens
				usage.OutputTokens = msg.Usage.CompletionTokens
				usage.TotalTokens = msg.Usage.TotalTokens
			}

			resp := protocol.ResponsesAPIResponse{
				ID:        responseID,
				Object:    "response",
				CreatedAt: created,
				Status:    "completed",
				Model:     resolvedModel,
				Output: []protocol.ResponsesOutputItem{
					{
						Type:    "message",
						ID:      messageID,
						Role:    "assistant",
						Content: []protocol.ResponsesContentPart{{Type: "output_text", Text: text}},
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

func (h *Handler) HandleEmbeddings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	rInstance, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		sendOpenAIError(w, "Invalid request body", http.StatusBadRequest, "invalid_request_error", "")
		return
	}

	var req protocol.EmbeddingRequest
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

	resolvedModel, exists := rInstance.ResolveModelID(req.Model)
	if !exists {
		sendOpenAIError(w, fmt.Sprintf("Model '%s' not found", req.Model), http.StatusNotFound, "invalid_request_error", "model_not_found")
		return
	}

	if !rInstance.IsConnected() {
		sendOpenAIError(w, "Extension not connected", http.StatusServiceUnavailable, "server_error", "")
		return
	}

	requestID := uuid.NewString()
	resultChan := make(chan *protocol.ExtensionMessage, 1)
	errChan := make(chan error, 1)

	pe := &room.PendingEmbedding{
		RequestID:  requestID,
		ResultChan: resultChan,
		ErrChan:    errChan,
	}

	rInstance.AddPendingEmbedding(pe)
	defer rInstance.RemovePendingEmbedding(requestID)

	wsReq := protocol.EmbeddingRequestMessage{
		Type:      "embeddingRequest",
		RequestID: requestID,
		Request:   req,
	}
	if err := rInstance.SendWSJSON(wsReq); err != nil {
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
		embData := make([]protocol.EmbeddingData, 0, len(msg.Embeddings))
		for i, emb := range msg.Embeddings {
			embData = append(embData, protocol.EmbeddingData{
				Object:    "embedding",
				Index:     i,
				Embedding: emb,
			})
		}

		usage := protocol.UsageInfo{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0}
		if msg.Usage != nil {
			usage = *msg.Usage
		}

		resp := protocol.EmbeddingResponse{
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

func (h *Handler) HandleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	rInstance, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/models")
	path = strings.TrimPrefix(path, "/models")
	path = strings.TrimPrefix(path, "/")

	if path != "" {
		model, found := rInstance.GetModel(path)
		if !found {
			sendOpenAIError(w, fmt.Sprintf("Model '%s' not found", path), http.StatusNotFound, "invalid_request_error", "model_not_found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.ModelObject{
			ID:      model.ID,
			Object:  "model",
			Created: model.Created,
			OwnedBy: model.OwnedBy,
		})
		return
	}

	models := rInstance.GetModels()
	data := make([]protocol.ModelObject, 0, len(models))
	for _, m := range models {
		data = append(data, protocol.ModelObject{
			ID:      m.ID,
			Object:  "model",
			Created: m.Created,
			OwnedBy: m.OwnedBy,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(protocol.ModelsListResponse{
		Object: "list",
		Data:   data,
	})
}
