package usage

import (
	"fmt"
	"strings"
	"sync"
)

// Price holds per-million-token prices in USD.
type Price struct {
	InputPerMillion  float64 // USD per 1M input tokens
	OutputPerMillion float64 // USD per 1M output tokens
}

// Cost calculates the cost for a given Usage.
func (p Price) Cost(u Usage) Cost {
	inputCost := float64(u.InputTokens) / 1_000_000 * p.InputPerMillion
	outputCost := float64(u.OutputTokens) / 1_000_000 * p.OutputPerMillion
	return Cost{
		InputCost:  inputCost,
		OutputCost: outputCost,
		TotalCost:  inputCost + outputCost,
		Currency:   "USD",
	}
}

// Cost is the calculated cost for a request.
type Cost struct {
	InputCost  float64 `json:"input_cost"`
	OutputCost float64 `json:"output_cost"`
	TotalCost  float64 `json:"total_cost"`
	Currency   string  `json:"currency"`
}

// PriceTable maps model names to their pricing.
// Thread-safe for concurrent reads and writes.
type PriceTable struct {
	mu     sync.RWMutex
	prices map[string]Price
}

// NewPriceTable creates a PriceTable pre-loaded with known model prices.
func NewPriceTable() *PriceTable {
	pt := &PriceTable{
		prices: make(map[string]Price),
	}
	pt.loadDefaults()
	return pt
}

// Lookup finds the price for a model. It tries exact match first,
// then prefix match (e.g. "gpt-4o-2024-08-06" matches "gpt-4o").
// Returns the Price and true if found.
func (pt *PriceTable) Lookup(model string) (Price, bool) {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	// Exact match
	if p, ok := pt.prices[model]; ok {
		return p, true
	}

	// Prefix match: find the longest prefix that matches
	var bestKey string
	var bestPrice Price
	for key, p := range pt.prices {
		if strings.HasPrefix(model, key) && len(key) > len(bestKey) {
			bestKey = key
			bestPrice = p
		}
	}
	if bestKey != "" {
		return bestPrice, true
	}

	return Price{}, false
}

// Set adds or updates a model's pricing.
func (pt *PriceTable) Set(model string, price Price) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.prices[model] = price
}

// Calculate returns the cost for a Usage, looking up the model's price.
// Returns an error if the model is not in the price table.
func (pt *PriceTable) Calculate(u Usage) (Cost, error) {
	p, ok := pt.Lookup(u.Model)
	if !ok {
		return Cost{}, fmt.Errorf("no price for model %q", u.Model)
	}
	return p.Cost(u), nil
}

// loadDefaults populates the table with known model prices.
// Prices as of early 2025. Update periodically.
func (pt *PriceTable) loadDefaults() {
	defaults := map[string]Price{
		// Anthropic Claude
		"claude-3-5-sonnet":    {InputPerMillion: 3.00, OutputPerMillion: 15.00},
		"claude-sonnet-4":      {InputPerMillion: 3.00, OutputPerMillion: 15.00},
		"claude-3-5-haiku":     {InputPerMillion: 0.80, OutputPerMillion: 4.00},
		"claude-3-opus":        {InputPerMillion: 15.00, OutputPerMillion: 75.00},

		// OpenAI
		"gpt-4o":               {InputPerMillion: 2.50, OutputPerMillion: 10.00},
		"gpt-4o-mini":          {InputPerMillion: 0.15, OutputPerMillion: 0.60},
		"gpt-4-turbo":          {InputPerMillion: 10.00, OutputPerMillion: 30.00},
		"gpt-4":                {InputPerMillion: 30.00, OutputPerMillion: 60.00},
		"gpt-3.5-turbo":        {InputPerMillion: 0.50, OutputPerMillion: 1.50},
		"o1":                   {InputPerMillion: 15.00, OutputPerMillion: 60.00},
		"o1-mini":              {InputPerMillion: 3.00, OutputPerMillion: 12.00},
		"o3-mini":              {InputPerMillion: 1.10, OutputPerMillion: 4.40},

		// DeepSeek
		"deepseek-chat":        {InputPerMillion: 0.27, OutputPerMillion: 1.10},
		"deepseek-reasoner":    {InputPerMillion: 0.55, OutputPerMillion: 2.19},

		// Google Gemini
		"gemini-2.0-flash":     {InputPerMillion: 0.10, OutputPerMillion: 0.40},
		"gemini-1.5-pro":       {InputPerMillion: 1.25, OutputPerMillion: 5.00},
		"gemini-1.5-flash":     {InputPerMillion: 0.075, OutputPerMillion: 0.30},

		// Ollama / local (free)
		"llama":                {InputPerMillion: 0, OutputPerMillion: 0},
		"qwen":                 {InputPerMillion: 0, OutputPerMillion: 0},
		"mistral":              {InputPerMillion: 0, OutputPerMillion: 0},
		"phi":                  {InputPerMillion: 0, OutputPerMillion: 0},
		"deepseek-r1":          {InputPerMillion: 0, OutputPerMillion: 0},
	}

	for model, price := range defaults {
		pt.prices[model] = price
	}
}
