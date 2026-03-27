package usage

import (
	"math"
	"testing"
)

func TestPriceTable_ExactMatch(t *testing.T) {
	pt := NewPriceTable()

	p, ok := pt.Lookup("gpt-4o")
	if !ok {
		t.Fatal("expected gpt-4o to be in price table")
	}
	if p.InputPerMillion != 2.50 {
		t.Errorf("InputPerMillion = %f, want 2.50", p.InputPerMillion)
	}
	if p.OutputPerMillion != 10.00 {
		t.Errorf("OutputPerMillion = %f, want 10.00", p.OutputPerMillion)
	}
}

func TestPriceTable_PrefixMatch(t *testing.T) {
	pt := NewPriceTable()

	// "gpt-4o-2024-08-06" should match "gpt-4o"
	p, ok := pt.Lookup("gpt-4o-2024-08-06")
	if !ok {
		t.Fatal("expected prefix match for gpt-4o-2024-08-06")
	}
	if p.InputPerMillion != 2.50 {
		t.Errorf("InputPerMillion = %f, want 2.50", p.InputPerMillion)
	}
}

func TestPriceTable_PrefixMatch_LongestWins(t *testing.T) {
	pt := NewPriceTable()

	// "gpt-4o-mini-2024-07-18" should match "gpt-4o-mini" (longer) not "gpt-4o"
	p, ok := pt.Lookup("gpt-4o-mini-2024-07-18")
	if !ok {
		t.Fatal("expected prefix match for gpt-4o-mini-*")
	}
	if p.InputPerMillion != 0.15 {
		t.Errorf("InputPerMillion = %f, want 0.15 (gpt-4o-mini price)", p.InputPerMillion)
	}
}

func TestPriceTable_NotFound(t *testing.T) {
	pt := NewPriceTable()

	_, ok := pt.Lookup("some-unknown-model")
	if ok {
		t.Error("expected no match for unknown model")
	}
}

func TestPriceTable_Set(t *testing.T) {
	pt := NewPriceTable()

	pt.Set("my-custom-model", Price{InputPerMillion: 1.0, OutputPerMillion: 2.0})

	p, ok := pt.Lookup("my-custom-model")
	if !ok {
		t.Fatal("expected custom model to be in table")
	}
	if p.InputPerMillion != 1.0 {
		t.Errorf("InputPerMillion = %f, want 1.0", p.InputPerMillion)
	}
}

func TestPrice_Cost(t *testing.T) {
	p := Price{InputPerMillion: 3.00, OutputPerMillion: 15.00}
	u := Usage{
		Model:        "claude-sonnet-4-20250514",
		InputTokens:  1000,
		OutputTokens: 500,
		TotalTokens:  1500,
	}

	c := p.Cost(u)

	// 1000 / 1M * $3 = $0.003
	assertFloat(t, "InputCost", c.InputCost, 0.003)
	// 500 / 1M * $15 = $0.0075
	assertFloat(t, "OutputCost", c.OutputCost, 0.0075)
	assertFloat(t, "TotalCost", c.TotalCost, 0.0105)
	assertEqual(t, "Currency", c.Currency, "USD")
}

func TestPriceTable_Calculate(t *testing.T) {
	pt := NewPriceTable()

	u := Usage{
		Model:        "gpt-4o",
		InputTokens:  10000,
		OutputTokens: 5000,
		TotalTokens:  15000,
	}

	c, err := pt.Calculate(u)
	if err != nil {
		t.Fatalf("Calculate error: %v", err)
	}

	// 10000 / 1M * $2.50 = $0.025
	assertFloat(t, "InputCost", c.InputCost, 0.025)
	// 5000 / 1M * $10 = $0.05
	assertFloat(t, "OutputCost", c.OutputCost, 0.05)
	assertFloat(t, "TotalCost", c.TotalCost, 0.075)
}

func TestPriceTable_Calculate_UnknownModel(t *testing.T) {
	pt := NewPriceTable()

	u := Usage{Model: "unknown-model-v99"}
	_, err := pt.Calculate(u)
	if err == nil {
		t.Error("expected error for unknown model")
	}
}

func TestPriceTable_Calculate_FreeModel(t *testing.T) {
	pt := NewPriceTable()

	u := Usage{
		Model:        "llama3.2",
		InputTokens:  100000,
		OutputTokens: 50000,
		TotalTokens:  150000,
	}

	c, err := pt.Calculate(u)
	if err != nil {
		t.Fatalf("Calculate error: %v", err)
	}

	assertFloat(t, "TotalCost", c.TotalCost, 0)
}

func TestPriceTable_Calculate_PrefixMatch(t *testing.T) {
	pt := NewPriceTable()

	u := Usage{
		Model:        "claude-sonnet-4-20250514",
		InputTokens:  1_000_000,
		OutputTokens: 500_000,
		TotalTokens:  1_500_000,
	}

	c, err := pt.Calculate(u)
	if err != nil {
		t.Fatalf("Calculate error: %v", err)
	}

	// 1M * $3/M = $3
	assertFloat(t, "InputCost", c.InputCost, 3.00)
	// 500K * $15/M = $7.50
	assertFloat(t, "OutputCost", c.OutputCost, 7.50)
}

func TestPriceTable_Anthropic(t *testing.T) {
	pt := NewPriceTable()

	tests := []struct {
		model  string
		input  float64
		output float64
	}{
		{"claude-3-5-sonnet-20241022", 3.00, 15.00},
		{"claude-sonnet-4-20250514", 3.00, 15.00},
		{"claude-3-5-haiku-20241022", 0.80, 4.00},
		{"claude-3-opus-20240229", 15.00, 75.00},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			p, ok := pt.Lookup(tt.model)
			if !ok {
				t.Fatalf("model %q not found", tt.model)
			}
			assertFloat(t, "InputPerMillion", p.InputPerMillion, tt.input)
			assertFloat(t, "OutputPerMillion", p.OutputPerMillion, tt.output)
		})
	}
}

func TestPriceTable_DeepSeek(t *testing.T) {
	pt := NewPriceTable()

	p, ok := pt.Lookup("deepseek-chat")
	if !ok {
		t.Fatal("deepseek-chat not found")
	}
	assertFloat(t, "InputPerMillion", p.InputPerMillion, 0.27)
	assertFloat(t, "OutputPerMillion", p.OutputPerMillion, 1.10)
}

func assertFloat(t *testing.T, field string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %f, want %f", field, got, want)
	}
}
