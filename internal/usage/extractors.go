package usage

import "encoding/json"

// OpenAIExtractor handles OpenAI, DeepSeek, and Ollama response formats.
// All three use the same structure:
//
//	{
//	  "model": "...",
//	  "usage": {
//	    "prompt_tokens": N,
//	    "completion_tokens": N,
//	    "total_tokens": N
//	  }
//	}
type OpenAIExtractor struct{}

type openAIResponse struct {
	Model string       `json:"model"`
	Usage *openAIUsage `json:"usage"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (OpenAIExtractor) Extract(body []byte) Usage {
	var resp openAIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Usage{}
	}
	if resp.Usage == nil {
		return Usage{}
	}
	total := resp.Usage.TotalTokens
	if total == 0 {
		total = resp.Usage.PromptTokens + resp.Usage.CompletionTokens
	}
	return Usage{
		Model:        resp.Model,
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		TotalTokens:  total,
	}
}

func (e OpenAIExtractor) ExtractSSEChunk(chunk []byte) Usage {
	// SSE format: "data: {json}\n\n"
	// The last chunk with stream_options.include_usage=true has usage data.
	data := extractSSEData(chunk)
	if data == nil {
		return Usage{}
	}
	return e.Extract(data)
}

// AnthropicExtractor handles Anthropic Claude response format:
//
//	{
//	  "model": "...",
//	  "usage": {
//	    "input_tokens": N,
//	    "output_tokens": N
//	  }
//	}
type AnthropicExtractor struct{}

type anthropicResponse struct {
	Model string          `json:"model"`
	Usage *anthropicUsage `json:"usage"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (AnthropicExtractor) Extract(body []byte) Usage {
	var resp anthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Usage{}
	}
	if resp.Usage == nil {
		return Usage{}
	}
	return Usage{
		Model:        resp.Model,
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		TotalTokens:  resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}
}

func (e AnthropicExtractor) ExtractSSEChunk(chunk []byte) Usage {
	// Anthropic SSE: message_delta event contains usage in the final chunk
	data := extractSSEData(chunk)
	if data == nil {
		return Usage{}
	}
	// message_delta: {"type":"message_delta","usage":{"output_tokens":N}}
	// message_start: {"type":"message_start","message":{"model":"...","usage":{"input_tokens":N}}}
	var msg struct {
		Type    string `json:"type"`
		Usage   *anthropicUsage `json:"usage"`
		Message *struct {
			Model string          `json:"model"`
			Usage *anthropicUsage `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return Usage{}
	}

	switch msg.Type {
	case "message_start":
		if msg.Message != nil && msg.Message.Usage != nil {
			return Usage{
				Model:       msg.Message.Model,
				InputTokens: msg.Message.Usage.InputTokens,
			}
		}
	case "message_delta":
		if msg.Usage != nil {
			return Usage{
				OutputTokens: msg.Usage.OutputTokens,
			}
		}
	}
	return Usage{}
}

// GeminiExtractor handles Google Gemini response format:
//
//	{
//	  "modelVersion": "...",
//	  "usageMetadata": {
//	    "promptTokenCount": N,
//	    "candidatesTokenCount": N,
//	    "totalTokenCount": N
//	  }
//	}
type GeminiExtractor struct{}

type geminiResponse struct {
	ModelVersion  string              `json:"modelVersion"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

func (GeminiExtractor) Extract(body []byte) Usage {
	var resp geminiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Usage{}
	}
	if resp.UsageMetadata == nil {
		return Usage{}
	}
	total := resp.UsageMetadata.TotalTokenCount
	if total == 0 {
		total = resp.UsageMetadata.PromptTokenCount + resp.UsageMetadata.CandidatesTokenCount
	}
	return Usage{
		Model:        resp.ModelVersion,
		InputTokens:  resp.UsageMetadata.PromptTokenCount,
		OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
		TotalTokens:  total,
	}
}

func (e GeminiExtractor) ExtractSSEChunk(chunk []byte) Usage {
	// Gemini streaming: each chunk may contain usageMetadata
	data := extractSSEData(chunk)
	if data == nil {
		return Usage{}
	}
	return e.Extract(data)
}
