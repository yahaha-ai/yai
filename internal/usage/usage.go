package usage

// Usage contains normalized token usage from any LLM provider.
type Usage struct {
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	TotalTokens  int    `json:"total_tokens"`
}

// Extractor extracts token usage from a response body.
// Implementations are provider-specific.
type Extractor interface {
	// Extract parses token usage from a non-streaming response body.
	// Returns zero Usage if usage info is not found (not an error).
	Extract(body []byte) Usage

	// ExtractSSEChunk inspects a single SSE chunk for usage info.
	// Returns zero Usage if this chunk doesn't contain usage data.
	// For streaming, call this on each chunk; the last non-zero result wins.
	ExtractSSEChunk(chunk []byte) Usage
}
