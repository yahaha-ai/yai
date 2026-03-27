package usage

import (
	"testing"
)

func TestOpenAIExtractor_Extract(t *testing.T) {
	body := []byte(`{
		"id": "chatcmpl-abc",
		"model": "gpt-4o",
		"usage": {
			"prompt_tokens": 100,
			"completion_tokens": 50,
			"total_tokens": 150
		}
	}`)

	var e OpenAIExtractor
	u := e.Extract(body)

	assertEqual(t, "Model", u.Model, "gpt-4o")
	assertEqualInt(t, "InputTokens", u.InputTokens, 100)
	assertEqualInt(t, "OutputTokens", u.OutputTokens, 50)
	assertEqualInt(t, "TotalTokens", u.TotalTokens, 150)
}

func TestOpenAIExtractor_Extract_NoUsage(t *testing.T) {
	body := []byte(`{"model": "gpt-4o"}`)

	var e OpenAIExtractor
	u := e.Extract(body)

	if u.InputTokens != 0 || u.OutputTokens != 0 {
		t.Errorf("expected zero usage, got %+v", u)
	}
}

func TestOpenAIExtractor_Extract_DeepSeek(t *testing.T) {
	body := []byte(`{
		"model": "deepseek-chat",
		"usage": {
			"prompt_tokens": 200,
			"completion_tokens": 100,
			"total_tokens": 300
		}
	}`)

	var e OpenAIExtractor
	u := e.Extract(body)

	assertEqual(t, "Model", u.Model, "deepseek-chat")
	assertEqualInt(t, "InputTokens", u.InputTokens, 200)
	assertEqualInt(t, "OutputTokens", u.OutputTokens, 100)
	assertEqualInt(t, "TotalTokens", u.TotalTokens, 300)
}

func TestOpenAIExtractor_Extract_ComputeTotal(t *testing.T) {
	// Some providers don't return total_tokens
	body := []byte(`{
		"model": "gpt-4",
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 5
		}
	}`)

	var e OpenAIExtractor
	u := e.Extract(body)

	assertEqualInt(t, "TotalTokens", u.TotalTokens, 15)
}

func TestOpenAIExtractor_SSE(t *testing.T) {
	chunk := []byte("data: {\"model\":\"gpt-4o\",\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":20,\"total_tokens\":30}}\n\n")

	var e OpenAIExtractor
	u := e.ExtractSSEChunk(chunk)

	assertEqual(t, "Model", u.Model, "gpt-4o")
	assertEqualInt(t, "InputTokens", u.InputTokens, 10)
	assertEqualInt(t, "OutputTokens", u.OutputTokens, 20)
}

func TestOpenAIExtractor_SSE_Done(t *testing.T) {
	chunk := []byte("data: [DONE]\n\n")

	var e OpenAIExtractor
	u := e.ExtractSSEChunk(chunk)

	if u.InputTokens != 0 {
		t.Errorf("expected zero usage for [DONE], got %+v", u)
	}
}

func TestAnthropicExtractor_Extract(t *testing.T) {
	body := []byte(`{
		"model": "claude-sonnet-4-20250514",
		"usage": {
			"input_tokens": 150,
			"output_tokens": 75
		}
	}`)

	var e AnthropicExtractor
	u := e.Extract(body)

	assertEqual(t, "Model", u.Model, "claude-sonnet-4-20250514")
	assertEqualInt(t, "InputTokens", u.InputTokens, 150)
	assertEqualInt(t, "OutputTokens", u.OutputTokens, 75)
	assertEqualInt(t, "TotalTokens", u.TotalTokens, 225)
}

func TestAnthropicExtractor_SSE_MessageStart(t *testing.T) {
	chunk := []byte(`data: {"type":"message_start","message":{"model":"claude-sonnet-4-20250514","usage":{"input_tokens":100}}}` + "\n\n")

	var e AnthropicExtractor
	u := e.ExtractSSEChunk(chunk)

	assertEqual(t, "Model", u.Model, "claude-sonnet-4-20250514")
	assertEqualInt(t, "InputTokens", u.InputTokens, 100)
}

func TestAnthropicExtractor_SSE_MessageDelta(t *testing.T) {
	chunk := []byte(`data: {"type":"message_delta","usage":{"output_tokens":50}}` + "\n\n")

	var e AnthropicExtractor
	u := e.ExtractSSEChunk(chunk)

	assertEqualInt(t, "OutputTokens", u.OutputTokens, 50)
}

func TestAnthropicExtractor_SSE_ContentBlock(t *testing.T) {
	// Content block deltas should return zero usage
	chunk := []byte(`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}` + "\n\n")

	var e AnthropicExtractor
	u := e.ExtractSSEChunk(chunk)

	if u.InputTokens != 0 || u.OutputTokens != 0 {
		t.Errorf("expected zero usage for content_block_delta, got %+v", u)
	}
}

func TestGeminiExtractor_Extract(t *testing.T) {
	body := []byte(`{
		"modelVersion": "gemini-1.5-pro",
		"usageMetadata": {
			"promptTokenCount": 200,
			"candidatesTokenCount": 100,
			"totalTokenCount": 300
		}
	}`)

	var e GeminiExtractor
	u := e.Extract(body)

	assertEqual(t, "Model", u.Model, "gemini-1.5-pro")
	assertEqualInt(t, "InputTokens", u.InputTokens, 200)
	assertEqualInt(t, "OutputTokens", u.OutputTokens, 100)
	assertEqualInt(t, "TotalTokens", u.TotalTokens, 300)
}

func TestGeminiExtractor_Extract_ComputeTotal(t *testing.T) {
	body := []byte(`{
		"modelVersion": "gemini-2.0-flash",
		"usageMetadata": {
			"promptTokenCount": 50,
			"candidatesTokenCount": 25
		}
	}`)

	var e GeminiExtractor
	u := e.Extract(body)

	assertEqualInt(t, "TotalTokens", u.TotalTokens, 75)
}

func TestGeminiExtractor_SSE(t *testing.T) {
	chunk := []byte(`data: {"modelVersion":"gemini-1.5-pro","usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}` + "\n\n")

	var e GeminiExtractor
	u := e.ExtractSSEChunk(chunk)

	assertEqual(t, "Model", u.Model, "gemini-1.5-pro")
	assertEqualInt(t, "TotalTokens", u.TotalTokens, 15)
}

func TestForProvider(t *testing.T) {
	tests := []struct {
		provider string
		expected string
	}{
		{"anthropic", "AnthropicExtractor"},
		{"gemini", "GeminiExtractor"},
		{"vertex", "GeminiExtractor"},
		{"openai", "OpenAIExtractor"},
		{"deepseek", "OpenAIExtractor"},
		{"ollama", "OpenAIExtractor"},
		{"groq", "OpenAIExtractor"},
		{"unknown", "OpenAIExtractor"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			e := ForProvider(tt.provider)
			switch tt.expected {
			case "AnthropicExtractor":
				if _, ok := e.(AnthropicExtractor); !ok {
					t.Errorf("ForProvider(%q) = %T, want AnthropicExtractor", tt.provider, e)
				}
			case "GeminiExtractor":
				if _, ok := e.(GeminiExtractor); !ok {
					t.Errorf("ForProvider(%q) = %T, want GeminiExtractor", tt.provider, e)
				}
			case "OpenAIExtractor":
				if _, ok := e.(OpenAIExtractor); !ok {
					t.Errorf("ForProvider(%q) = %T, want OpenAIExtractor", tt.provider, e)
				}
			}
		})
	}
}

func TestExtractSSEData(t *testing.T) {
	tests := []struct {
		name  string
		chunk []byte
		want  bool // whether data should be returned
	}{
		{"normal data line", []byte("data: {\"test\":1}\n\n"), true},
		{"DONE marker", []byte("data: [DONE]\n\n"), false},
		{"empty data", []byte("data: \n\n"), false},
		{"event line only", []byte("event: message_start\n\n"), false},
		{"multiline with event and data", []byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"), true},
		{"no data prefix", []byte("just text\n"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := extractSSEData(tt.chunk)
			if tt.want && data == nil {
				t.Error("expected data, got nil")
			}
			if !tt.want && data != nil {
				t.Errorf("expected nil, got %q", data)
			}
		})
	}
}

func TestExtract_InvalidJSON(t *testing.T) {
	body := []byte("not json at all")

	extractors := []struct {
		name string
		e    Extractor
	}{
		{"OpenAI", OpenAIExtractor{}},
		{"Anthropic", AnthropicExtractor{}},
		{"Gemini", GeminiExtractor{}},
	}

	for _, ext := range extractors {
		t.Run(ext.name, func(t *testing.T) {
			u := ext.e.Extract(body)
			if u.InputTokens != 0 || u.OutputTokens != 0 {
				t.Errorf("expected zero usage for invalid JSON, got %+v", u)
			}
		})
	}
}

// Helpers

func assertEqual(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func assertEqualInt(t *testing.T, field string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d, want %d", field, got, want)
	}
}
