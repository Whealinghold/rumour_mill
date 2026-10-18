package knowledge

import (
	"errors"
	"fmt"
)

// Errors returned by this package. Check with errors.Is.
var (
	ErrInvalidFact      = errors.New("knowledge: invalid fact")
	ErrIdentityMismatch = errors.New("knowledge: fact ID reused with a different type or subject")
	ErrInvalidConfig    = errors.New("knowledge: invalid config")
)

// Config holds the tunables of a Store. Use DefaultConfig and override fields.
type Config struct {
	// SourceRanks gives each source kind a rank; higher wins merges. Replace the
	// map wholesale with your own vocabulary.
	SourceRanks map[string]int `json:"source_ranks" yaml:"source_ranks"`
	// UnrankedRank is the rank of a source kind absent from SourceRanks.
	UnrankedRank int `json:"unranked_rank" yaml:"unranked_rank"`
	// QueryFloor is the default minimum confidence for Find.
	QueryFloor float64 `json:"query_floor" yaml:"query_floor"`
	// MaxFacts caps the facts in one store; 0 means unlimited.
	MaxFacts int `json:"max_facts" yaml:"max_facts"`
	// Fade configures forgetting for Store.Fade.
	Fade FadeConfig `json:"fade" yaml:"fade"`
}

// DefaultConfig returns the documented defaults. Each call returns a fresh
// copy, including the SourceRanks map, so callers may mutate it.
func DefaultConfig() Config {
	return Config{
		SourceRanks:  map[string]int{"observed": 3, "relayed": 2, "derived": 1},
		UnrankedRank: 0,
		QueryFloor:   0.2,
		MaxFacts:     0,
		Fade:         FadeConfig{HalfLives: map[string]int64{}},
	}
}

// Validate reports every problem with c, joined; each wraps ErrInvalidConfig.
func (c Config) Validate() error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: "+format, append([]any{ErrInvalidConfig}, args...)...))
	}
	if !(c.QueryFloor >= 0 && c.QueryFloor <= 1) {
		bad("query_floor %v outside [0,1]", c.QueryFloor)
	}
	if c.MaxFacts < 0 {
		bad("max_facts %d is negative", c.MaxFacts)
	}
	for name := range c.SourceRanks {
		if name == "" {
			bad("source_ranks has an empty source name")
		}
	}
	for name, h := range c.Fade.HalfLives {
		if name == "" {
			bad("fade.half_lives has an empty source name")
		}
		if h < 0 {
			bad("fade.half_lives[%q] %d is negative", name, h)
		}
	}
	if c.Fade.DefaultHalfLife < 0 {
		bad("fade.default_half_life %d is negative", c.Fade.DefaultHalfLife)
	}
	if !(c.Fade.ForgetBelow >= 0 && c.Fade.ForgetBelow <= 1) {
		bad("fade.forget_below %v outside [0,1]", c.Fade.ForgetBelow)
	}
	return errors.Join(errs...)
}

// FadeConfig configures fading: ForgetBelow, and the built-in exponential
// half-life policy used when no WithFader is given. Without a positive
// half-life anywhere there is no fader and Fade does nothing.
// Like the rest of Config it is plain data: NewStore clones HalfLives, so later
// changes to the caller's map do not reach a live store, and a FadeConfig is
// not safe to mutate concurrently with reading it.
type FadeConfig struct {
	// HalfLives is the half-life in ticks per source kind; 0 = never fades.
	HalfLives map[string]int64 `json:"half_lives" yaml:"half_lives"`
	// DefaultHalfLife applies to source kinds absent from HalfLives; 0 = never.
	DefaultHalfLife int64 `json:"default_half_life" yaml:"default_half_life"`
	// ForgetBelow forgets a fact whose faded confidence is below it; 0 = none.
	ForgetBelow float64 `json:"forget_below" yaml:"forget_below"`
}
