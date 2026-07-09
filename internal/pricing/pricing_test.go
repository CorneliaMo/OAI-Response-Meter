package pricing

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func floatPtr(value float64) *float64 { return &value }

func TestEstimatePricedUsage(t *testing.T) {
	catalog := &Catalog{
		Currency: "USD",
		Unit:     UnitPer1MTokens,
		Models: map[string]Rate{
			"gpt-test": {Input: 2, CachedInput: 0.2, CacheWriteInput: floatPtr(2.5), Output: 8},
		},
	}
	got := catalog.Estimate(Usage{
		Model:            "gpt-test",
		InputTokens:      1_000_000,
		CachedTokens:     200_000,
		CacheWriteTokens: 100_000,
		OutputTokens:     500_000,
		TotalTokens:      1_500_000,
	})
	if got.Status != "priced" || got.Currency != "USD" {
		t.Fatalf("Estimate() = %+v", got)
	}
	want := 700_000.0/1_000_000*2 + 200_000.0/1_000_000*0.2 + 100_000.0/1_000_000*2.5 + 500_000.0/1_000_000*8
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
					{MinInputTokens: 272001, Input: 5, CachedInput: 0.5, CacheWriteInput: floatPtr(0.75), Output: 20},
				},
			},
		},
	}

	got := catalog.Estimate(Usage{
		Model:            "gpt-test",
		InputTokens:      272_001,
		CachedTokens:     72_001,
		CacheWriteTokens: 50_000,
		OutputTokens:     100_000,
		TotalTokens:      372_001,
	})

	want := 150_000.0/1_000_000*5 + 72_001.0/1_000_000*0.5 + 50_000.0/1_000_000*0.75 + 100_000.0/1_000_000*20
	if math.Abs(got.EstimatedCost-want) > 0.000001 {
		t.Fatalf("EstimatedCost = %f, want %f", got.EstimatedCost, want)
	}
}

func TestEstimateUsesSelectedTierForCachedAndCacheWriteInput(t *testing.T) {
	catalog := &Catalog{
		Currency: "USD",
		Unit:     UnitPer1MTokens,
		Models: map[string]Rate{
			"gpt-test": {
				Input:           2,
				CachedInput:     0.2,
				CacheWriteInput: floatPtr(2.5),
				Output:          8,
				Tiers: []Tier{
					{MinInputTokens: 100_000, Input: 3, CachedInput: 0.3, CacheWriteInput: floatPtr(3.75), Output: 12},
					{MinInputTokens: 272_001, Input: 5, CachedInput: 0.5, CacheWriteInput: floatPtr(6.25), Output: 20},
				},
			},
		},
	}

	got := catalog.Estimate(Usage{
		Model:            "gpt-test",
		InputTokens:      300_000,
		CachedTokens:     150_000,
		CacheWriteTokens: 25_000,
		OutputTokens:     50_000,
		TotalTokens:      350_000,
	})

	want := 125_000.0/1_000_000*5 + 150_000.0/1_000_000*0.5 + 25_000.0/1_000_000*6.25 + 50_000.0/1_000_000*20
	if math.Abs(got.EstimatedCost-want) > 0.000001 {
		t.Fatalf("EstimatedCost = %f, want %f", got.EstimatedCost, want)
	}
}

func TestEstimateFallsBackToInputRateForCacheWriteWhenOmitted(t *testing.T) {
	catalog := &Catalog{
		Currency: "USD",
		Unit:     UnitPer1MTokens,
		Models: map[string]Rate{
			"gpt-test": {Input: 2, CachedInput: 0.2, Output: 8},
		},
	}
	got := catalog.Estimate(Usage{
		Model:            "gpt-test",
		InputTokens:      100_000,
		CachedTokens:     20_000,
		CacheWriteTokens: 10_000,
		OutputTokens:     50_000,
		TotalTokens:      150_000,
	})
	want := 70_000.0/1_000_000*2 + 20_000.0/1_000_000*0.2 + 10_000.0/1_000_000*2 + 50_000.0/1_000_000*8
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

func TestEstimateCanonicalizesDatedModelSnapshots(t *testing.T) {
	catalog := &Catalog{
		Currency: "USD",
		Unit:     UnitPer1MTokens,
		Models: map[string]Rate{
			"gpt-5.4":      {Input: 2, CachedInput: 0.2, Output: 8},
			"gpt-5.4-mini": {Input: 0.75, CachedInput: 0.075, Output: 4.5},
		},
	}
	got := catalog.Estimate(Usage{Model: "gpt-5.4-mini-2026-03-17", InputTokens: 1_000_000, TotalTokens: 1_000_000})
	if got.Status != "priced" || math.Abs(got.EstimatedCost-0.75) > 0.000001 {
		t.Fatalf("snapshot cost = %+v", got)
	}

	got = catalog.Estimate(Usage{Model: "gpt-5.4-mini-preview", InputTokens: 1_000_000, TotalTokens: 1_000_000})
	if got.Status != "unpriced" {
		t.Fatalf("non-dated suffix should not match: %+v", got)
	}
}

func TestCanonicalModelNameRequiresExactValidDateSuffix(t *testing.T) {
	tests := map[string]string{
		"gpt-5.4-mini-2026-03-17": "gpt-5.4-mini",
		"gpt-5.4-mini-2026-13-17": "gpt-5.4-mini-2026-13-17",
		"gpt-5.4-mini-preview":    "gpt-5.4-mini-preview",
		"gpt-5.4-mini":            "gpt-5.4-mini",
	}
	for input, want := range tests {
		if got := CanonicalModelName(input); got != want {
			t.Fatalf("CanonicalModelName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLoadValidatesCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(path, []byte(`{"currency":"USD","unit":"per_1m_tokens","models":{"gpt-test":{"input":1,"cached_input":0.1,"cache_write_input":1.25,"output":2}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	catalog, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if catalog.Models["gpt-test"].Output != 2 || catalog.Models["gpt-test"].CacheWriteInput == nil || *catalog.Models["gpt-test"].CacheWriteInput != 1.25 {
		t.Fatalf("catalog = %+v", catalog)
	}
}

func TestLoadValidatesInvalidTiers(t *testing.T) {
	tests := []string{
		`{"currency":"USD","unit":"per_1m_tokens","models":{"gpt-test":{"input":1,"cached_input":0.1,"output":2,"tiers":[{"min_input_tokens":0,"input":5,"cached_input":0.5,"output":10}]}}}`,
		`{"currency":"USD","unit":"per_1m_tokens","models":{"gpt-test":{"input":1,"cached_input":0.1,"output":2,"tiers":[{"min_input_tokens":10,"input":5,"cached_input":0.5,"output":10},{"min_input_tokens":10,"input":6,"cached_input":0.6,"output":12}]}}}`,
		`{"currency":"USD","unit":"per_1m_tokens","models":{"gpt-test":{"input":1,"cached_input":0.1,"output":2,"tiers":[{"min_input_tokens":10,"input":-1,"cached_input":0.5,"output":10}]}}}`,
		`{"currency":"USD","unit":"per_1m_tokens","models":{"gpt-test":{"input":1,"cached_input":0.1,"cache_write_input":-1,"output":2}}}`,
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
