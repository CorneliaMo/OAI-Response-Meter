package pricing

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
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
	Input       float64 `json:"input"`
	CachedInput float64 `json:"cached_input"`
	Output      float64 `json:"output"`
}

type Usage struct {
	Model        string
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	TotalTokens  int64
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
		if rate.Input < 0 || rate.CachedInput < 0 || rate.Output < 0 {
			return fmt.Errorf("prices for %q must be non-negative", model)
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
	rate, ok := c.Models[usage.Model]
	if !ok {
		cost.UnpricedTokens = usage.TotalTokens
		return cost
	}
	billableInput := usage.InputTokens - usage.CachedTokens
	if billableInput < 0 {
		billableInput = 0
	}
	cost.Status = "priced"
	cost.PricedTokens = usage.TotalTokens
	cost.EstimatedCost =
		float64(billableInput)/1_000_000*rate.Input +
			float64(usage.CachedTokens)/1_000_000*rate.CachedInput +
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
