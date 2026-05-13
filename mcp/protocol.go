package mcp

import (
	"encoding/json"
	"sync"
	"time"
)

// --- JSON-RPC types ---

// Request is a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

// Error is a JSON-RPC 2.0 error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- MCP protocol types ---

// InitializeParams holds the client's initialize request parameters.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      ClientInfo         `json:"clientInfo"`
}

// ClientInfo describes the client identity.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientCapabilities describes what the client supports.
type ClientCapabilities struct {
	Elicitation *ElicitationCapability `json:"elicitation,omitempty"`
	Sampling    *SamplingCapability    `json:"sampling,omitempty"`
	Roots       *RootsCapability       `json:"roots,omitempty"`
}

// ElicitationCapability indicates the client supports elicitation/create.
type ElicitationCapability struct{}

// SamplingCapability indicates the client supports sampling/createMessage.
type SamplingCapability struct{}

// RootsCapability indicates the client supports roots/list.
type RootsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// InitializeResult is the response to the initialize method.
type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    Capabilities `json:"capabilities"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
}

// Capabilities describes the server's MCP capabilities.
type Capabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Logging   *LoggingCapability   `json:"logging,omitempty"`
}

// ToolsCapability indicates the server supports tool listing and calling.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// PromptsCapability indicates the server supports prompt listing and retrieval.
type PromptsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// ResourcesCapability indicates the server supports resource listing and reading.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe"`
	ListChanged bool `json:"listChanged"`
}

// LoggingCapability indicates the server supports logging notifications.
type LoggingCapability struct{}

// ServerInfo describes the server identity.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// --- Tools types ---

// ToolsListResult is the response to tools/list.
type ToolsListResult struct {
	Tools []ToolDef `json:"tools"`
}

// ToolDef describes a single tool for tools/list.
type ToolDef struct {
	Name         string           `json:"name"`
	Title        string           `json:"title,omitempty"`
	Description  string           `json:"description"`
	InputSchema  InputSchema      `json:"inputSchema"`
	OutputSchema interface{}      `json:"outputSchema,omitempty"`
	Annotations  *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations provides metadata hints about a tool's behavior.
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

// InputSchema is a JSON Schema describing the tool's input parameters.
type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

// Property describes a single input parameter.
type Property struct {
	Type        string    `json:"type"`
	Description string    `json:"description,omitempty"`
	Items       *Property `json:"items,omitempty"`
}

// ToolCallParams is the params object for tools/call.
type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ToolCallResult is the response to tools/call.
type ToolCallResult struct {
	Content           []ContentBlock `json:"content"`
	StructuredContent interface{}    `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
}

// ContentBlock is a single content block in a tool result.
// Supports "text" and "resource_link" types per MCP 2025-11-25.
type ContentBlock struct {
	Type        string             `json:"type"`
	Text        string             `json:"text,omitempty"`
	URI         string             `json:"uri,omitempty"`
	Name        string             `json:"name,omitempty"`
	Description string             `json:"description,omitempty"`
	MimeType    string             `json:"mimeType,omitempty"`
	Annotations *ContentAnnotation `json:"annotations,omitempty"`
}

// ContentAnnotation provides hints about a content block's audience and priority.
type ContentAnnotation struct {
	Audience []string `json:"audience,omitempty"` // "user", "assistant"
	Priority float64  `json:"priority,omitempty"` // 0.0 - 1.0
}

// --- Elicitation types ---

// ElicitFunc asks the client a question and returns the user's response.
// If the client does not support elicitation (or we are in HTTP mode), this
// will be nil and tool handlers should proceed with defaults.
type ElicitFunc func(message string, schema map[string]interface{}) (map[string]interface{}, error)

// SamplingFunc asks the client's LLM to reason about a prompt via
// sampling/createMessage (MCP 2025-11-25). Returns the LLM's text response.
// Nil when the client does not declare the sampling capability.
type SamplingFunc func(messages []SamplingMessage, maxTokens int) (string, error)

// ProgressFunc sends a progress notification for a long-running tool call.
// progress and total follow the MCP notifications/progress spec.
// message is a human-readable status update shown in the client UI.
// Calls are fire-and-forget — errors are silently discarded.
// May be nil if the server has no notification writer (e.g. HTTP transport).
type ProgressFunc func(progress, total float64, message string)

// SamplingMessage is a single message in a sampling/createMessage request.
type SamplingMessage struct {
	Role    string          `json:"role"`
	Content SamplingContent `json:"content"`
}

// SamplingContent is the content of a sampling message.
type SamplingContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// SamplingRequest is the JSON-RPC request for sampling/createMessage.
type SamplingRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      any               `json:"id"`
	Method  string            `json:"method"`
	Params  SamplingReqParams `json:"params"`
}

// SamplingReqParams is the params for sampling/createMessage.
type SamplingReqParams struct {
	Messages  []SamplingMessage `json:"messages"`
	MaxTokens int               `json:"maxTokens"`
}

// SamplingResponse is the client's response to a sampling/createMessage request.
type SamplingResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Result  SamplingResult `json:"result"`
	Error   *Error         `json:"error,omitempty"`
}

// SamplingResult is the result of a sampling/createMessage response.
type SamplingResult struct {
	Role    string          `json:"role"`
	Content SamplingContent `json:"content"`
	Model   string          `json:"model,omitempty"`
}

// ElicitationRequest is the JSON-RPC request sent to the client.
type ElicitationRequest struct {
	JSONRPC string               `json:"jsonrpc"`
	ID      any                  `json:"id"`
	Method  string               `json:"method"`
	Params  ElicitationReqParams `json:"params"`
}

// ElicitationReqParams is the params for elicitation/create.
type ElicitationReqParams struct {
	Message         string      `json:"message"`
	RequestedSchema interface{} `json:"requestedSchema"`
}

// ElicitationResponse is the client's response to an elicitation request.
type ElicitationResponse struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      any               `json:"id,omitempty"`
	Result  ElicitationResult `json:"result"`
	Error   *Error            `json:"error,omitempty"`
}

// ElicitationResult is the result of an elicitation/create response.
type ElicitationResult struct {
	Action  string                 `json:"action"` // "accept", "decline", "cancel"
	Content map[string]interface{} `json:"content,omitempty"`
}

// --- Progress notification types ---

// ProgressNotification is sent during long-running operations.
type ProgressNotification struct {
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  ProgressParams `json:"params"`
}

// ProgressParams describes progress of an operation.
type ProgressParams struct {
	ProgressToken string  `json:"progressToken"`
	Progress      float64 `json:"progress"`
	Total         float64 `json:"total"`
	Message       string  `json:"message,omitempty"`
}

// --- Logging notification types ---

// LogNotification is a server-to-client log message.
type LogNotification struct {
	JSONRPC string    `json:"jsonrpc"`
	Method  string    `json:"method"`
	Params  LogParams `json:"params"`
}

// LogParams describes a log message.
type LogParams struct {
	Level  string `json:"level"`  // "debug", "info", "warning", "error"
	Logger string `json:"logger"` // logger name (e.g., "trvl")
	Data   string `json:"data"`   // log message
}

// --- Prompts types ---

// PromptsListResult is the response to prompts/list.
type PromptsListResult struct {
	Prompts []PromptDef `json:"prompts"`
}

// PromptDef describes a prompt template.
type PromptDef struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument describes a single prompt argument.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// PromptsGetParams is the params object for prompts/get.
type PromptsGetParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// PromptsGetResult is the response to prompts/get.
type PromptsGetResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// PromptMessage is a single message in a prompt result.
type PromptMessage struct {
	Role    string       `json:"role"`
	Content ContentBlock `json:"content"`
}

// --- Resources types ---

// ResourcesListResult is the response to resources/list.
type ResourcesListResult struct {
	Resources []ResourceDef `json:"resources"`
}

// ResourceDef describes a single resource.
type ResourceDef struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ResourcesReadParams is the params object for resources/read.
type ResourcesReadParams struct {
	URI string `json:"uri"`
}

// ResourcesReadResult is the response to resources/read.
type ResourcesReadResult struct {
	Contents []ResourceContent `json:"contents"`
}

// ResourceContent is a single content block in a resource read result.
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// --- Trip state types ---

// TripState tracks all searches in the current session for trip summary.
type TripState struct {
	mu          sync.Mutex
	Searches    []SearchRecord  `json:"searches"`
	Shortlisted []ShortlistItem `json:"shortlisted"`
}

// SearchRecord captures one tool call for the trip summary.
type SearchRecord struct {
	Type      string    `json:"type"`       // "flight", "hotel", "destination"
	Query     string    `json:"query"`      // human-readable search description
	BestPrice float64   `json:"best_price"` // cheapest result price
	Currency  string    `json:"currency"`
	Time      time.Time `json:"time"`
}

// ShortlistItem tracks a user-interesting result.
type ShortlistItem struct {
	Type        string  `json:"type"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	Currency    string  `json:"currency"`
}

// --- Price cache for watch resources ---

// priceCache stores the last known price for a route, for delta tracking.
type priceCache struct {
	mu     sync.Mutex
	prices map[string]float64 // key: "origin-dest-date" -> price
}

func newPriceCache() *priceCache {
	return &priceCache{prices: make(map[string]float64)}
}

func (c *priceCache) get(key string) (float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.prices[key]
	return v, ok
}

func (c *priceCache) set(key string, price float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prices[key] = price
}
