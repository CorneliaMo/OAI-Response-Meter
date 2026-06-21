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
