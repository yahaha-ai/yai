package usage

import "bytes"

// extractSSEData extracts the JSON data from an SSE "data: ..." line.
// Returns nil if the chunk doesn't contain a data line or is "[DONE]".
func extractSSEData(chunk []byte) []byte {
	// SSE chunks may contain multiple lines; find the "data: " line
	for _, line := range bytes.Split(chunk, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))
			data = bytes.TrimSpace(data)
			if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
				continue
			}
			return data
		}
	}
	return nil
}

// ForProvider returns the appropriate Extractor for a provider name.
// Falls back to OpenAIExtractor for unknown providers (most LLM APIs
// use the OpenAI-compatible format).
func ForProvider(providerName string) Extractor {
	switch providerName {
	case "anthropic":
		return AnthropicExtractor{}
	case "gemini", "vertex":
		return GeminiExtractor{}
	default:
		// OpenAI-compatible: openai, deepseek, ollama, groq, together, etc.
		return OpenAIExtractor{}
	}
}
