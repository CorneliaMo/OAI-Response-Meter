package pricing

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const UnitPer1MTokens = "per_1m_tokens"

type Catalog struct {
	Currency    string          `json:"currency"`
	Unit        string          `json:"unit"`
	LastChecked string          `json:"last_checked"`
	Source      string          `json:"source"`
	Models      map[string]Rate `json:"models"`
}

type Rate struct {
	Input           float64  `json:"input"`
	CachedInput     float64  `json:"cached_input"`
	CacheWriteInput *float64 `json:"cache_write_input,omitempty"`
	Output          float64  `json:"output"`
	Tiers           []Tier   `json:"tiers,omitempty"`
}

type Tier struct {
	MinInputTokens  int64    `json:"min_input_tokens"`
	Input           float64  `json:"input"`
	CachedInput     float64  `json:"cached_input"`
	CacheWriteInput *float64 `json:"cache_write_input,omitempty"`
	Output          float64  `json:"output"`
}

type Usage struct {
	Model            string
	InputTokens      int64
	OutputTokens     int64
	CachedTokens     int64
	CacheWriteTokens int64
	TotalTokens      int64
}

type Cost struct {
	Enabled        bool    `json:"pricing_enabled"`
	Status         string  `json:"pricing_status"`
	Currency       string  `json:"currency"`
	EstimatedCost  float64 `json:"estimated_cost"`
	PricedTokens   int64   `json:"priced_tokens"`
	UnpricedTokens int64   `json:"unpriced_tokens"`
}

func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var catalog Catalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("parse prices: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func (c *Catalog) Validate() error {
	if c == nil {
		return nil
	}
	if strings.TrimSpace(c.Currency) == "" {
		return errors.New("prices currency is required")
	}
	if c.Unit != UnitPer1MTokens {
		return fmt.Errorf("prices unit must be %q", UnitPer1MTokens)
	}
	for model, rate := range c.Models {
		if strings.TrimSpace(model) == "" {
			return errors.New("prices model name is required")
		}
		if rate.Input < 0 || rate.CachedInput < 0 || rate.Output < 0 || valueOrZero(rate.CacheWriteInput) < 0 {
			return fmt.Errorf("prices for %q must be non-negative", model)
		}
		var lastMin int64
		for i, tier := range rate.Tiers {
			if tier.MinInputTokens <= 0 {
				return fmt.Errorf("prices tier %d for %q must have positive min_input_tokens", i, model)
			}
			if tier.Input < 0 || tier.CachedInput < 0 || tier.Output < 0 || valueOrZero(tier.CacheWriteInput) < 0 {
				return fmt.Errorf("prices tier %d for %q must be non-negative", i, model)
			}
			if i > 0 && tier.MinInputTokens <= lastMin {
				return fmt.Errorf("prices tiers for %q must have strictly increasing min_input_tokens", model)
			}
			lastMin = tier.MinInputTokens
		}
	}
	return nil
}

func (c *Catalog) Estimate(usage Usage) Cost {
	if c == nil {
		return Cost{Enabled: false, Status: "disabled", UnpricedTokens: usage.TotalTokens}
	}
	cost := Cost{
		Enabled:  true,
		Status:   "unpriced",
		Currency: c.Currency,
	}
	rate, ok := c.Models[CanonicalModelName(usage.Model)]
	if !ok {
		cost.UnpricedTokens = usage.TotalTokens
		return cost
	}
	rate = rate.selectFor(usage.InputTokens)
	normalInput := usage.InputTokens - usage.CachedTokens - usage.CacheWriteTokens
	if normalInput < 0 {
		normalInput = 0
	}
	cost.Status = "priced"
	cost.PricedTokens = usage.TotalTokens
	cost.EstimatedCost =
		float64(normalInput)/1_000_000*rate.Input +
			float64(usage.CachedTokens)/1_000_000*rate.CachedInput +
			float64(usage.CacheWriteTokens)/1_000_000*rate.cacheWriteInputRate() +
			float64(usage.OutputTokens)/1_000_000*rate.Output
	return cost
}

func Add(a, b Cost) Cost {
	if b.Enabled {
		a.Enabled = true
	}
	if a.Currency == "" {
		a.Currency = b.Currency
	}
	a.EstimatedCost += b.EstimatedCost
	a.PricedTokens += b.PricedTokens
	a.UnpricedTokens += b.UnpricedTokens
	if !a.Enabled {
		a.Status = "disabled"
	} else if a.UnpricedTokens > 0 && a.PricedTokens > 0 {
		a.Status = "partial"
	} else if a.UnpricedTokens > 0 {
		a.Status = "unpriced"
	} else {
		a.Status = "priced"
	}
	return a
}

func (r Rate) selectFor(inputTokens int64) Rate {
	selected := Rate{
		Input:           r.Input,
		CachedInput:     r.CachedInput,
		CacheWriteInput: r.CacheWriteInput,
		Output:          r.Output,
	}
	for _, tier := range r.Tiers {
		if inputTokens < tier.MinInputTokens {
			break
		}
		selected = Rate{
			Input:           tier.Input,
			CachedInput:     tier.CachedInput,
			CacheWriteInput: tier.CacheWriteInput,
			Output:          tier.Output,
		}
	}
	return selected
}

func (r Rate) cacheWriteInputRate() float64 {
	if r.CacheWriteInput != nil {
		return *r.CacheWriteInput
	}
	return r.Input
}

func (r Rate) CacheWriteInputRate() float64 {
	return r.cacheWriteInputRate()
}

func (t Tier) CacheWriteInputRate() float64 {
	if t.CacheWriteInput != nil {
		return *t.CacheWriteInput
	}
	return t.Input
}

func valueOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

// CanonicalModelName folds dated model snapshots into their base model name.
// Only an exact, valid -YYYY-MM-DD suffix is removed, so model families that
// merely share a prefix remain distinct.
func CanonicalModelName(model string) string {
	if len(model) < len("-2006-01-02") {
		return model
	}
	suffixStart := len(model) - len("2006-01-02")
	if suffixStart == 0 || model[suffixStart-1] != '-' {
		return model
	}
	if _, err := time.Parse("2006-01-02", model[suffixStart:]); err != nil {
		return model
	}
	return model[:suffixStart-1]
}
