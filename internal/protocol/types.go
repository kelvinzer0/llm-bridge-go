package protocol

// === Model Definition ===

type ModelDefinition struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	OwnedBy     string `json:"owned_by"`
	Description string `json:"description,omitempty"`
	Created     int64  `json:"created,omitempty"`
}

// === Tool Definitions ===

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function ToolCallFunction `json:"function"`
}

type ToolDefinition struct {
	Type     string                 `json:"type"` // "function"
	Function ToolFunctionDefinition `json:"function"`
}

type ToolFunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// === Extension Messages (Extension -> Server) ===

type ExtensionMessage struct {
	Type         string            `json:"type"`
	Models       []ModelDefinition `json:"models,omitempty"`
	IDs          []string          `json:"ids,omitempty"`
	RequestID    string            `json:"requestId,omitempty"`
	Delta        *StreamDelta      `json:"delta,omitempty"`
	Content      *string           `json:"content,omitempty"`
	ToolCalls    []ToolCall        `json:"tool_calls,omitempty"`
	FinishReason *string           `json:"finish_reason,omitempty"`
	Usage        *UsageInfo        `json:"usage,omitempty"`
	Error        string            `json:"error,omitempty"`
	Embeddings   [][]float64       `json:"embeddings,omitempty"`
}

type StreamDelta struct {
	Content string `json:"content"`
	Role    string `json:"role,omitempty"`
}

// === Server Messages (Server -> Extension) ===

type CompletionRequestMessage struct {
	Type      string                `json:"type"`
	RequestID string                `json:"requestId"`
	Request   ChatCompletionRequest `json:"request"`
}

type EmbeddingRequestMessage struct {
	Type      string           `json:"type"`
	RequestID string           `json:"requestId"`
	Request   EmbeddingRequest `json:"request"`
}

type ResponsesRequestMessage struct {
	Type      string              `json:"type"`
	RequestID string              `json:"requestId"`
	Request   ResponsesAPIRequest `json:"request"`
}

// === OpenAI Chat Completions ===

type ChatMessage struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content,omitempty"`
	Name       string      `json:"name,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
}

type ChatCompletionRequest struct {
	Model               string                 `json:"model"`
	Messages            []ChatMessage          `json:"messages"`
	Tools               []ToolDefinition       `json:"tools,omitempty"`
	ToolChoice          interface{}            `json:"tool_choice,omitempty"`
	Temperature         *float64               `json:"temperature,omitempty"`
	TopP                *float64               `json:"top_p,omitempty"`
	MaxTokens           *int                   `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int                   `json:"max_completion_tokens,omitempty"`
	Stream              bool                   `json:"stream,omitempty"`
	Stop                interface{}            `json:"stop,omitempty"`
	PresencePenalty     *float64               `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float64               `json:"frequency_penalty,omitempty"`
	N                   *int                   `json:"n,omitempty"`
	Extra               map[string]interface{} `json:"-"`
}

type ChatCompletionChoice struct {
	Index        int                         `json:"index"`
	Message      ChatCompletionChoiceMessage `json:"message"`
	FinishReason string                      `json:"finish_reason"`
}

type ChatCompletionChoiceMessage struct {
	Role      string     `json:"role"`
	Content   *string    `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"` // "chat.completion"
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   UsageInfo              `json:"usage"`
}

type ChatCompletionChunkChoice struct {
	Index        int                      `json:"index"`
	Delta        ChatCompletionChunkDelta `json:"delta"`
	FinishReason *string                  `json:"finish_reason"`
}

type ChatCompletionChunkDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   *string    `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ChatCompletionChunk struct {
	ID      string                      `json:"id"`
	Object  string                      `json:"object"` // "chat.completion.chunk"
	Created int64                       `json:"created"`
	Model   string                      `json:"model"`
	Choices []ChatCompletionChunkChoice `json:"choices"`
}

// === OpenAI Responses API ===

type ResponsesAPIRequest struct {
	Model           string      `json:"model"`
	Input           interface{} `json:"input"`
	Stream          bool        `json:"stream,omitempty"`
	Temperature     *float64    `json:"temperature,omitempty"`
	MaxOutputTokens *int        `json:"max_output_tokens,omitempty"`
}

type ResponsesContentPart struct {
	Type string `json:"type"` // "output_text"
	Text string `json:"text"`
}

type ResponsesOutputItem struct {
	Type    string                 `json:"type"` // "message"
	ID      string                 `json:"id"`
	Role    string                 `json:"role"` // "assistant"
	Content []ResponsesContentPart `json:"content"`
}

type ResponsesAPIResponse struct {
	ID        string                `json:"id"`
	Object    string                `json:"object"` // "response"
	CreatedAt int64                 `json:"created_at"`
	Status    string                `json:"status"`
	Model     string                `json:"model"`
	Output    []ResponsesOutputItem `json:"output"`
	Usage     *ResponsesUsage       `json:"usage,omitempty"`
}

type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// === OpenAI Embeddings ===

type EmbeddingRequest struct {
	Model      string      `json:"model"`
	Input      interface{} `json:"input"`
	Dimensions *int        `json:"dimensions,omitempty"`
}

type EmbeddingData struct {
	Object    string    `json:"object"` // "embedding"
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type EmbeddingResponse struct {
	Object string          `json:"object"` // "list"
	Data   []EmbeddingData `json:"data"`
	Model  string          `json:"model"`
	Usage  UsageInfo       `json:"usage"`
}

// === Models List ===

type ModelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"` // "model"
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ModelsListResponse struct {
	Object string        `json:"object"` // "list"
	Data   []ModelObject `json:"data"`
}

// === Shared & Errors ===

type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type OpenAIErrorResponse struct {
	Error OpenAIErrorDetail `json:"error"`
}

type OpenAIErrorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Code    *string `json:"code"`
	Param   *string `json:"param"`
}
