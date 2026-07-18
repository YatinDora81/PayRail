package domain

import (
	"encoding/json"
	"errors"
	"sort"
	"time"
)

var ErrNotStackable = errors.New("promotions cannot be combined")

type Rule struct {
	RuleType string
	Config   []byte
}

type RuleContext struct {
	PlanID          string
	Currency        string
	BaseAmountMinor int64
}

type Candidate struct {
	ID           string
	StackingMode string // EXCLUSIVE | STACKABLE
	Priority     int
	CreatedAt    time.Time
	Effects      []Effect
}

type Effect struct {
	EffectType   string // PERCENT_BPS | FLAT_AMOUNT | BONUS_CREDITS
	ValueBps     int
	AmountMinor  int64
	BonusCredits int
}

func RulesAllow(rules []Rule, rc RuleContext) bool {
	for _, r := range rules {
		switch r.RuleType {
		case "PLAN_IN": // config: {"planIds": ["plan_a", "plan_b"]}
			var cfg struct {
				PlanIDs []string `json:"planIds"`
			}
			if json.Unmarshal(r.Config, &cfg) != nil {
				return false
			}
			ok := false
			for _, id := range cfg.PlanIDs {
				if id == rc.PlanID {
					ok = true
					break
				}
			}
			if !ok {
				return false
			}
		case "MIN_AMOUNT_MINOR": // config: {"currency": "INR", "amountMinor": 50000}
			// Semantics (product-flagged, v1.3): a rule constrains ONLY its own
			// currency — author one per market. Cross-currency it is a no-op BY
			// DESIGN pending product's call (fail-closed was the alternative).
			var cfg struct {
				Currency    string `json:"currency"`
				AmountMinor int64  `json:"amountMinor"`
			}
			if json.Unmarshal(r.Config, &cfg) != nil {
				return false
			}
			if cfg.Currency == rc.Currency && rc.BaseAmountMinor < cfg.AmountMinor {
				return false
			}
		default:
			return false // fail CLOSED on rule types this build can't evaluate
		}
	}
	return true
}

type Contribution struct {
	PromotionID   string
	DiscountMinor int64
	BonusCredits  int
}

// Resolution is the engine's whole answer.
type Resolution struct {
	Contributions []Contribution
	DiscountMinor int64 // == Σ contributions, post-clamp — asserted in tests
	BonusCredits  int
}

func Resolve(cands []Candidate, base int64, maxDiscountBps int) (Resolution, error) {
	if len(cands) > 1 {
		for _, c := range cands {
			if c.StackingMode != "STACKABLE" {
				return Resolution{}, ErrNotStackable
			}
		}
	}

	raw := make([]Contribution, len(cands))
	bonus := 0
	var sum int64

	for i, c := range cands {
		var d int64
		b := 0
		for _, e := range c.Effects {
			switch e.EffectType {
			case "PERCENT_BPS":
				d += base * int64(e.ValueBps) / 10000 // floors — the ledger's favour
			case "FLAT_AMOUNT":
				if e.AmountMinor > base {
					d += base
				} else {
					d += e.AmountMinor
				}
			case "BONUS_CREDITS":
				b += e.BonusCredits
			default:
				// unknown effect type is inert, not fatal (as in v1.0)
			}
		}
		raw[i] = Contribution{PromotionID: c.ID, DiscountMinor: d, BonusCredits: b}
		sum += d
		bonus += b
	}

	total := clamp(sum, base, maxDiscountBps)

	idx := make([]int, len(cands))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ca, cb := cands[idx[a]], cands[idx[b]]
		if ca.Priority != cb.Priority {
			return ca.Priority > cb.Priority
		}
		if !ca.CreatedAt.Equal(cb.CreatedAt) {
			return ca.CreatedAt.Before(cb.CreatedAt)
		}
		return ca.ID < cb.ID
	})

	res := Resolution{DiscountMinor: total, BonusCredits: bonus}
	remaining := total
	for _, i := range idx {
		c := raw[i]
		if c.DiscountMinor > remaining {
			c.DiscountMinor = remaining // the promo the cap lands on: partial
		}
		remaining -= c.DiscountMinor
		res.Contributions = append(res.Contributions, c)
	}
	return res, nil
}

func clamp(discount, base int64, maxDiscountBps int) int64 {
	maxAllowed := base * int64(maxDiscountBps) / 10000
	if discount > maxAllowed {
		discount = maxAllowed
	}
	if discount > base {
		discount = base
	}
	if discount < 0 {
		discount = 0
	}
	return discount
}
