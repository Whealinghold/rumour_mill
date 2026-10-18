package knowledge

import (
	"errors"
	"reflect"
	"testing"
)

func mustStore(t *testing.T, cfg Config, opts ...Option) *Store {
	t.Helper()
	s, err := NewStore(cfg, opts...)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func fact(source string, confidence float64) Fact {
	return Fact{Type: "t", Subject: "s", Source: source, Confidence: confidence}
}

func TestDefaultConfigValues(t *testing.T) {
	want := Config{
		SourceRanks:  map[string]int{"observed": 3, "relayed": 2, "derived": 1},
		UnrankedRank: 0,
		QueryFloor:   0.2,
		MaxFacts:     0,
	}
	if got := DefaultConfig(); !reflect.DeepEqual(got, want) {
		t.Errorf("DefaultConfig() = %+v, want %+v", got, want)
	}
	if err := DefaultConfig().Validate(); err != nil {
		t.Errorf("DefaultConfig().Validate() = %v", err)
	}
}

func TestDefaultConfigReturnsFreshCopy(t *testing.T) {
	a := DefaultConfig()
	a.SourceRanks["observed"] = 99
	if b := DefaultConfig(); b.SourceRanks["observed"] != 3 {
		t.Errorf("mutating one DefaultConfig leaked into the next: %v", b.SourceRanks)
	}
}

func TestStoreCopiesConfig(t *testing.T) {
	cfg := DefaultConfig()
	s := mustStore(t, cfg)
	cfg.SourceRanks["derived"] = 99 // must not affect the store
	s.Add(fact("derived", 0.5))
	if r, _ := s.Add(fact("relayed", 0.1)); r.Outcome != Replaced {
		t.Errorf("relayed should still outrank derived, got %v", r.Outcome)
	}
}

// Each test below asserts a default and shows that overriding it changes behaviour.

func TestSourceRanksDefaultAndOverride(t *testing.T) {
	tests := []struct {
		name    string
		ranks   map[string]int // nil = default
		first   string
		second  string
		wantOut Outcome
	}{
		{"default observed beats relayed", nil, "relayed", "observed", Replaced},
		{"default relayed beats derived", nil, "derived", "relayed", Replaced},
		{"default derived loses to relayed", nil, "relayed", "derived", Rejected},
		{"override reverses order", map[string]int{"observed": 1, "relayed": 2, "derived": 3}, "relayed", "observed", Rejected},
		{"custom vocabulary", map[string]int{"witnessed": 3, "hearsay": 2}, "hearsay", "witnessed", Replaced},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			if tc.ranks != nil {
				cfg.SourceRanks = tc.ranks
			}
			s := mustStore(t, cfg)
			s.Add(fact(tc.first, 0.5))
			res, err := s.Add(fact(tc.second, 0.5))
			if err != nil || res.Outcome != tc.wantOut {
				t.Errorf("outcome = %v (err %v), want %v", res.Outcome, err, tc.wantOut)
			}
		})
	}
}

func TestUnrankedRankDefaultAndOverride(t *testing.T) {
	tests := []struct {
		name    string
		unrank  int
		wantOut Outcome // unknown source arriving over an existing "derived" (rank 1)
	}{
		{"default 0 loses to derived", 0, Rejected},
		{"override 5 beats derived", 5, Replaced},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.UnrankedRank = tc.unrank
			s := mustStore(t, cfg)
			s.Add(fact("derived", 0.5))
			res, _ := s.Add(fact("mystery", 0.9))
			if res.Outcome != tc.wantOut {
				t.Errorf("outcome = %v, want %v", res.Outcome, tc.wantOut)
			}
		})
	}
}

func TestQueryFloorDefaultAndOverride(t *testing.T) {
	tests := []struct {
		name    string
		floor   *float64 // nil = default 0.2
		conf    float64
		visible bool
	}{
		{"default: at the floor is visible", nil, 0.2, true},
		{"default: below the floor is hidden", nil, 0.19, false},
		{"override 0.5 hides 0.4", ptr(0.5), 0.4, false},
		{"override 0.5 shows 0.5", ptr(0.5), 0.5, true},
		{"override 0 shows everything", ptr(0.0), 0.01, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			if tc.floor != nil {
				cfg.QueryFloor = *tc.floor
			}
			s := mustStore(t, cfg)
			s.Add(fact("observed", tc.conf))
			if got := len(s.Find(Query{}, 0)) == 1; got != tc.visible {
				t.Errorf("visible = %v, want %v", got, tc.visible)
			}
		})
	}
}

func TestMaxFactsDefaultAndOverride(t *testing.T) {
	tests := []struct {
		name string
		max  int // 0 = default
		want int
	}{
		{"default is unlimited", 0, 50},
		{"override 3 caps the store", 3, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.MaxFacts = tc.max
			s := mustStore(t, cfg)
			for i := range 50 {
				s.Add(Fact{Type: "t", Subject: string(rune('a' + i)), Source: "observed", Confidence: 0.5})
			}
			if got := len(s.All()); got != tc.want {
				t.Errorf("stored %d facts, want %d", got, tc.want)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		bad    bool
	}{
		{"default", func(*Config) {}, false},
		{"floor 0 and 1 are fine", func(c *Config) { c.QueryFloor = 1 }, false},
		{"floor above 1", func(c *Config) { c.QueryFloor = 1.1 }, true},
		{"floor below 0", func(c *Config) { c.QueryFloor = -0.1 }, true},
		{"floor NaN", func(c *Config) { c.QueryFloor = nan() }, true},
		{"negative max facts", func(c *Config) { c.MaxFacts = -1 }, true},
		{"empty source name", func(c *Config) { c.SourceRanks[""] = 1 }, true},
		{"nil rank map is fine", func(c *Config) { c.SourceRanks = nil }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if (err != nil) != tc.bad {
				t.Fatalf("Validate() = %v, want error: %v", err, tc.bad)
			}
			if tc.bad && !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("error %v does not wrap ErrInvalidConfig", err)
			}
			if _, nerr := NewStore(cfg); (nerr != nil) != tc.bad {
				t.Errorf("NewStore err = %v, want error: %v", nerr, tc.bad)
			}
		})
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueryFloor = 2
	cfg.MaxFacts = -1
	if err := cfg.Validate(); err == nil || len(err.Error()) < 20 {
		t.Fatalf("expected a joined error, got %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func nan() float64 { var z float64; return z / z }

// The zero Config is useful: no ranks (every kind is unranked), no query
// floor (everything is visible) and no cap.
func TestZeroConfigWorks(t *testing.T) {
	s := mustStore(t, Config{})
	s.Add(fact("anything", 0.01))
	if got := len(s.Find(Query{}, 0)); got != 1 {
		t.Errorf("zero config hides facts: Find returned %d", got)
	}
	// All kinds share UnrankedRank, so equal-rank rules apply.
	if res, _ := s.Add(fact("other", 0.5)); res.Outcome != Replaced {
		t.Errorf("outcome = %v, want Replaced (equal rank, higher confidence)", res.Outcome)
	}
}
