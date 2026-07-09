package pricing

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestEstimatePricedUsage(t *testing.T) {
	catalog := &Catalog{
		Currency: "USD",
		Unit:     UnitPer1MTokens,
		Models: map[string]Rate{
			"gpt-test": {Input: 2, CachedInput: 0.2, Output: 8},
		},
	}
	got := catalog.Estimate(Usage{
		Model:        "gpt-test",
		InputTokens:  1_000_000,
		CachedTokens: 200_000,
		OutputTokens: 500_000,
		TotalTokens:  1_500_000,
	})
	if got.Status != "priced" || got.Currency != "USD" {
		t.Fatalf("Estimate() = %+v", got)
	}
	want := 800_000.0/1_000_000*2 + 200_000.0/1_000_000*0.2 + 500_000.0/1_000_000*8
	if math.Abs(got.EstimatedCost-want) > 0.000001 {
		t.Fatalf("EstimatedCost = %f, want %f", got.EstimatedCost, want)
	}
}

func TestEstimateUsesBaseRateBelowThreshold(t *testing.T) {
	catalog := &Catalog{
		Currency: "USD",
		Unit:     UnitPer1MTokens,
		Models: map[string]Rate{
			"gpt-test": {
				Input:       2,
				CachedInput: 0.2,
				Output:      8,
				Tiers: []Tier{
					{MinInputTokens: 272001, Input: 5, CachedInput: 0.5, Output: 20},
				},
			},
		},
	}

	got := catalog.Estimate(Usage{
		Model:        "gpt-test",
		InputTokens:  271_999,
		CachedTokens: 71_999,
		OutputTokens: 100_000,
		TotalTokens:  371_999,
	})

	want := 200_000.0/1_000_000*2 + 71_999.0/1_000_000*0.2 + 100_000.0/1_000_000*8
	if math.Abs(got.EstimatedCost-want) > 0.000001 {
		t.Fatalf("EstimatedCost = %f, want %f", got.EstimatedCost, want)
	}
}

func TestEstimateUsesBaseRateAtExactThreshold(t *testing.T) {
	catalog := &Catalog{
		Currency: "USD",
		Unit:     UnitPer1MTokens,
		Models: map[string]Rate{
			"gpt-test": {
				Input:       2,
				CachedInput: 0.2,
				Output:      8,
				Tiers: []Tier{
					{MinInputTokens: 272001, Input: 5, CachedInput: 0.5, Output: 20},
				},
			},
		},
	}

	got := catalog.Estimate(Usage{
		Model:        "gpt-test",
		InputTokens:  272_000,
		CachedTokens: 72_000,
		OutputTokens: 100_000,
		TotalTokens:  372_000,
	})

	want := 200_000.0/1_000_000*2 + 72_000.0/1_000_000*0.2 + 100_000.0/1_000_000*8
	if math.Abs(got.EstimatedCost-want) > 0.000001 {
		t.Fatalf("EstimatedCost = %f, want %f", got.EstimatedCost, want)
	}
}

func TestEstimateUsesTierRateAboveThreshold(t *testing.T) {
	catalog := &Catalog{
		Currency: "USD",
		Unit:     UnitPer1MTokens,
		Models: map[string]Rate{
			"gpt-test": {
				Input:       2,
				CachedInput: 0.2,
				Output:      8,
				Tiers: []Tier{
					{MinInputTokens: 272001, Input: 5, CachedInput: 0.5, Output: 20},
				},
			},
		},
	}

	got := catalog.Estimate(Usage{
		Model:        "gpt-test",
		InputTokens:  272_001,
		CachedTokens: 72_001,
		OutputTokens: 100_000,
		TotalTokens:  372_001,
	})

	want := 200_000.0/1_000_000*5 + 72_001.0/1_000_000*0.5 + 100_000.0/1_000_000*20
	if math.Abs(got.EstimatedCost-want) > 0.000001 {
		t.Fatalf("EstimatedCost = %f, want %f", got.EstimatedCost, want)
	}
}

func TestEstimateUsesSelectedTierForCachedInput(t *testing.T) {
	catalog := &Catalog{
		Currency: "USD",
		Unit:     UnitPer1MTokens,
		Models: map[string]Rate{
			"gpt-test": {
				Input:       2,
				CachedInput: 0.2,
				Output:      8,
				Tiers: []Tier{
					{MinInputTokens: 100_000, Input: 3, CachedInput: 0.3, Output: 12},
					{MinInputTokens: 272_001, Input: 5, CachedInput: 0.5, Output: 20},
				},
			},
		},
	}

	got := catalog.Estimate(Usage{
		Model:        "gpt-test",
		InputTokens:  300_000,
		CachedTokens: 150_000,
		OutputTokens: 50_000,
		TotalTokens:  350_000,
	})

	want := 150_000.0/1_000_000*5 + 150_000.0/1_000_000*0.5 + 50_000.0/1_000_000*20
	if math.Abs(got.EstimatedCost-want) > 0.000001 {
		t.Fatalf("EstimatedCost = %f, want %f", got.EstimatedCost, want)
	}
}

func TestEstimateUnpricedUsage(t *testing.T) {
	catalog := &Catalog{Currency: "USD", Unit: UnitPer1MTokens, Models: map[string]Rate{}}
	got := catalog.Estimate(Usage{Model: "missing", TotalTokens: 123})
	if got.Status != "unpriced" || got.UnpricedTokens != 123 {
		t.Fatalf("Estimate() = %+v", got)
	}
}

func TestLoadValidatesCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(path, []byte(`{"currency":"USD","unit":"per_1m_tokens","models":{"gpt-test":{"input":1,"cached_input":0.1,"output":2}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	catalog, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if catalog.Models["gpt-test"].Output != 2 {
		t.Fatalf("catalog = %+v", catalog)
	}
}

func TestLoadValidatesInvalidTiers(t *testing.T) {
	tests := []string{
		`{"currency":"USD","unit":"per_1m_tokens","models":{"gpt-test":{"input":1,"cached_input":0.1,"output":2,"tiers":[{"min_input_tokens":0,"input":5,"cached_input":0.5,"output":10}]}}}`,
		`{"currency":"USD","unit":"per_1m_tokens","models":{"gpt-test":{"input":1,"cached_input":0.1,"output":2,"tiers":[{"min_input_tokens":10,"input":5,"cached_input":0.5,"output":10},{"min_input_tokens":10,"input":6,"cached_input":0.6,"output":12}]}}}`,
		`{"currency":"USD","unit":"per_1m_tokens","models":{"gpt-test":{"input":1,"cached_input":0.1,"output":2,"tiers":[{"min_input_tokens":10,"input":-1,"cached_input":0.5,"output":10}]}}}`,
	}

	for _, content := range tests {
		path := filepath.Join(t.TempDir(), "prices.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("Load() unexpectedly succeeded for %s", content)
		}
	}
}
