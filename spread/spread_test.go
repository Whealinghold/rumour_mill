package spread_test

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/spread"
)

func kcfg() knowledge.Config { return knowledge.DefaultConfig() }

func mustSpreader(t *testing.T, cfg spread.Config, opts ...spread.Option) *spread.Spreader {
	t.Helper()
	sp, err := spread.New(cfg, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return sp
}

// give adds a first-hand fact to p.
func give(t *testing.T, p *person, id string, confidence float64) {
	t.Helper()
	if _, err := p.kb.Add(knowledge.Fact{ID: id, Type: "t", Subject: "s", Confidence: confidence, Source: "observed"}); err != nil {
		t.Fatal(err)
	}
}

func got(t *testing.T, p *person, id string) (knowledge.Fact, bool) {
	t.Helper()
	return p.kb.Get(id)
}

func TestDefaultConfigValues(t *testing.T) {
	want := spread.Config{DecayFactor: 0.8, MinConfidence: 0.2, MinSourceConfidence: 0.3, MaxHops: 0, ReceivedKind: "relayed", SkipBackToSource: true}
	if g := spread.DefaultConfig(); g != want {
		t.Errorf("DefaultConfig() = %+v, want %+v", g, want)
	}
	if err := spread.DefaultConfig().Validate(); err != nil {
		t.Errorf("default does not validate: %v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*spread.Config)
		bad    bool
	}{
		{"default", func(*spread.Config) {}, false},
		{"decay 1 is fine", func(c *spread.Config) { c.DecayFactor = 1 }, false},
		{"decay 0", func(c *spread.Config) { c.DecayFactor = 0 }, true},
		{"decay above 1", func(c *spread.Config) { c.DecayFactor = 1.01 }, true},
		{"decay NaN", func(c *spread.Config) { c.DecayFactor = math.NaN() }, true},
		{"min confidence below 0", func(c *spread.Config) { c.MinConfidence = -0.1 }, true},
		{"min confidence above 1", func(c *spread.Config) { c.MinConfidence = 1.1 }, true},
		{"min source confidence above 1", func(c *spread.Config) { c.MinSourceConfidence = 2 }, true},
		{"negative max hops", func(c *spread.Config) { c.MaxHops = -1 }, true},
		{"empty received kind", func(c *spread.Config) { c.ReceivedKind = "" }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := spread.DefaultConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if (err != nil) != tc.bad || (tc.bad && !errors.Is(err, spread.ErrInvalidConfig)) {
				t.Errorf("Validate() = %v, want error: %v", err, tc.bad)
			}
			if _, nerr := spread.New(cfg); (nerr != nil) != tc.bad {
				t.Errorf("New err = %v, want error: %v", nerr, tc.bad)
			}
		})
	}
	if err := (spread.Config{}).Validate(); err == nil {
		t.Error("the zero Config must be rejected; start from DefaultConfig")
	}
}

// Each test below asserts a default and shows that overriding it changes behaviour.

func TestDecayFactor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		factor *float64
		want   float64
	}{{"default 0.8", nil, 0.72}, {"override 0.5", ptr(0.5), 0.45}, {"override 1 keeps confidence", ptr(1.0), 0.9}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := spread.DefaultConfig()
			if tc.factor != nil {
				cfg.DecayFactor = *tc.factor
			}
			a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
			give(t, a, "f", 0.9)
			res, _ := mustSpreader(t, cfg).Spread(a, holders(b), 5)
			if len(res.Transfers) != 1 || math.Abs(res.Transfers[0].Confidence-tc.want) > 1e-9 {
				t.Fatalf("transfers = %+v, want one at %v", res.Transfers, tc.want)
			}
			if f, _ := got(t, b, "f"); math.Abs(f.Confidence-tc.want) > 1e-9 {
				t.Errorf("stored confidence %v, want %v", f.Confidence, tc.want)
			}
		})
	}
}

func TestMinConfidence(t *testing.T) {
	// 0.3 * 0.8 = 0.24 clears the default floor of 0.2; 0.3 * 0.5 = 0.15 does not.
	tests := []struct {
		name      string
		min       *float64
		decay     float64
		conf      float64
		delivered bool
	}{
		{"default: above floor", nil, 0.8, 0.3, true},
		{"default: below floor", nil, 0.5, 0.3, false},
		{"override 0.3 rejects 0.24", ptr(0.3), 0.8, 0.3, false},
		{"override 0 delivers 0.15", ptr(0.0), 0.5, 0.3, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := spread.DefaultConfig()
			cfg.DecayFactor = tc.decay
			if tc.min != nil {
				cfg.MinConfidence = *tc.min
			}
			a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
			give(t, a, "f", tc.conf)
			res, _ := mustSpreader(t, cfg).Spread(a, holders(b), 0)
			if (len(res.Transfers) == 1) != tc.delivered {
				t.Errorf("transfers = %+v, want delivered: %v", res.Transfers, tc.delivered)
			}
		})
	}
}

func TestMinSourceConfidence(t *testing.T) {
	tests := []struct {
		name  string
		floor *float64
		conf  float64
		told  bool
	}{
		{"default: at 0.3 is offered", nil, 0.3, true},
		{"default: below 0.3 is not", nil, 0.29, false},
		{"override 0.6 withholds 0.5", ptr(0.6), 0.5, false},
		{"override 0.1 offers 0.15", ptr(0.1), 0.15, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := spread.DefaultConfig()
			cfg.DecayFactor = 1 // isolate the teller floor from the delivery floor
			cfg.MinConfidence = 0
			if tc.floor != nil {
				cfg.MinSourceConfidence = *tc.floor
			}
			// Lower the teller's own query floor so only the spread floor decides.
			c := kcfg()
			c.QueryFloor = 0
			a, b := newPerson("a", c), newPerson("b", kcfg())
			give(t, a, "f", tc.conf)
			res, _ := mustSpreader(t, cfg).Spread(a, holders(b), 0)
			if (len(res.Transfers) == 1) != tc.told {
				t.Errorf("transfers = %+v, want told: %v", res.Transfers, tc.told)
			}
		})
	}
}

func TestMaxHops(t *testing.T) {
	run := func(maxHops int) (reached int) {
		cfg := spread.DefaultConfig()
		cfg.DecayFactor = 1
		cfg.MaxHops = maxHops
		sp := mustSpreader(t, cfg)
		chain := make([]*person, 8)
		for i := range chain {
			chain[i] = newPerson(fmt.Sprint("p", i), kcfg())
		}
		give(t, chain[0], "f", 0.9)
		for i := 0; i+1 < len(chain); i++ {
			if res, _ := sp.Spread(chain[i], holders(chain[i+1]), 0); len(res.Transfers) == 1 {
				reached = i + 1
			}
		}
		return reached
	}
	tests := []struct{ maxHops, wantReached int }{{0, 7}, {1, 1}, {3, 3}, {5, 5}}
	for _, tc := range tests {
		if g := run(tc.maxHops); g != tc.wantReached {
			t.Errorf("max_hops %d: reached person %d, want %d (0 = unlimited)", tc.maxHops, g, tc.wantReached)
		}
	}
}

func TestReceivedKind(t *testing.T) {
	for _, tc := range []struct{ kind, want string }{{"", "relayed"}, {"grapevine", "grapevine"}} {
		cfg := spread.DefaultConfig()
		if tc.kind != "" {
			cfg.ReceivedKind = tc.kind
		}
		a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
		give(t, a, "f", 0.9)
		mustSpreader(t, cfg).Spread(a, holders(b), 0)
		if f, _ := got(t, b, "f"); f.Source != tc.want {
			t.Errorf("kind %q: source %q, want %q", tc.kind, f.Source, tc.want)
		}
	}
}

func TestSkipBackToSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		skip bool
		want int
	}{{"default skips", true, 0}, {"override tells them anyway", false, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := spread.DefaultConfig()
			cfg.SkipBackToSource = tc.skip
			sp := mustSpreader(t, cfg)
			a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
			give(t, a, "f", 0.9)
			sp.Spread(a, holders(b), 0)
			res, _ := sp.Spread(b, holders(a), 1) // b heard it from a
			if len(res.Transfers) != tc.want {
				t.Errorf("transfers back to the source = %d, want %d", len(res.Transfers), tc.want)
			}
		})
	}
}

func TestProvenanceAndCopy(t *testing.T) {
	exp := knowledge.Tick(900)
	a, b, c := newPerson("a", kcfg()), newPerson("b", kcfg()), newPerson("c", kcfg())
	a.kb.Add(knowledge.Fact{ID: "f", Type: "t", Subject: "s", Discriminator: "d", Payload: "p", Confidence: 0.9, Source: "observed",
		Expiry: &exp, Prov: knowledge.Provenance{EventID: "ev-7", Premises: []string{"x"}}, Observed: 3})
	sp := mustSpreader(t, spread.DefaultConfig())
	sp.Spread(a, holders(b), 10)
	sp.Spread(b, holders(c), 20)

	fb, _ := got(t, b, "f")
	fc, _ := got(t, c, "f")
	want := knowledge.Fact{ID: "f", Type: "t", Subject: "s", Discriminator: "d", Payload: "p", Confidence: fc.Confidence, Source: "relayed",
		Expiry: &exp, Observed: 20, Prov: knowledge.Provenance{From: "b", Hops: 2, EventID: "ev-7"}}
	if !reflect.DeepEqual(fc, want) {
		t.Errorf("c holds %+v, want %+v", fc, want)
	}
	if fb.Prov.From != "a" || fb.Prov.Hops != 1 || fb.Observed != 10 || len(fb.Prov.Premises) != 0 {
		t.Errorf("b holds %+v", fb)
	}
	if fa, _ := got(t, a, "f"); fa.Source != "observed" || fa.Confidence != 0.9 || fa.Prov.Hops != 0 {
		t.Errorf("the teller's own fact changed: %+v", fa)
	}
	// Delivered copies do not alias the teller's expiry pointer.
	*fb.Expiry = 1
	if fa, _ := got(t, a, "f"); *fa.Expiry != 900 {
		t.Error("expiry aliased")
	}
}

func TestOffersAreFiltered(t *testing.T) {
	exp := knowledge.Tick(50)
	a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
	a.kb.Add(knowledge.Fact{ID: "public", Type: "t", Confidence: 0.9, Source: "observed"})
	a.kb.Add(knowledge.Fact{ID: "private", Type: "t", Confidence: 0.9, Source: "observed", Private: true})
	a.kb.Add(knowledge.Fact{ID: "expired", Type: "t", Confidence: 0.9, Source: "observed", Expiry: &exp})
	a.kb.Add(knowledge.Fact{ID: "weak", Type: "t", Confidence: 0.25, Source: "observed"})
	res, _ := mustSpreader(t, spread.DefaultConfig()).Spread(a, holders(b), 50)
	if len(res.Transfers) != 1 || res.Transfers[0].FactID != "public" {
		t.Errorf("transfers = %+v, want only the public fact", res.Transfers)
	}
	if f, ok := got(t, b, "private"); ok {
		t.Errorf("a private fact reached the listener: %+v", f)
	}
}

func TestPrivateNeverSpreadsEvenIfFiltersAllow(t *testing.T) {
	a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
	a.kb.Add(knowledge.Fact{ID: "p", Type: "t", Confidence: 1, Source: "observed", Private: true})
	sp := mustSpreader(t, spread.DefaultConfig(), spread.WithFilter(func(spread.Encounter) bool { return true }))
	sp.Transform = func(f knowledge.Fact) knowledge.Fact { f.Private = false; return f }
	if res, _ := sp.Spread(a, holders(b), 0); len(res.Transfers) != 0 {
		t.Errorf("private fact spread: %+v", res.Transfers)
	}
}

func TestFilters(t *testing.T) {
	var order []string
	record := func(name string, ok bool) func(spread.Encounter) bool {
		return func(e spread.Encounter) bool { order = append(order, name); return ok }
	}
	tests := []struct {
		name    string
		filters []func(spread.Encounter) bool
		want    int
		calls   []string
	}{
		{"none", nil, 1, nil},
		{"all pass", []func(spread.Encounter) bool{record("a", true), record("b", true)}, 1, []string{"a", "b"}},
		{"first blocks and short-circuits", []func(spread.Encounter) bool{record("a", false), record("b", true)}, 0, []string{"a"}},
		{"second blocks", []func(spread.Encounter) bool{record("a", true), record("b", false)}, 0, []string{"a", "b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			order = nil
			var opts []spread.Option
			for _, f := range tc.filters {
				opts = append(opts, spread.WithFilter(f))
			}
			opts = append(opts, spread.WithFilter(nil)) // ignored
			a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
			give(t, a, "f", 0.9)
			res, _ := mustSpreader(t, spread.DefaultConfig(), opts...).Spread(a, holders(b), 0)
			if len(res.Transfers) != tc.want || !slices.Equal(order, tc.calls) {
				t.Errorf("transfers %d (want %d), filters called %v (want %v)", len(res.Transfers), tc.want, order, tc.calls)
			}
		})
	}
}

func TestFilterSeesTheEncounter(t *testing.T) {
	a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
	give(t, a, "f", 0.9)
	var seen spread.Encounter
	sp := mustSpreader(t, spread.DefaultConfig(), spread.WithFilter(func(e spread.Encounter) bool { seen = e; return true }))
	sp.Spread(a, holders(b), 42)
	if seen.Teller.ID() != "a" || seen.Listener.ID() != "b" || seen.Fact.ID != "f" || seen.Fact.Confidence != 0.9 || seen.Now != 42 {
		t.Errorf("encounter = %+v; the fact must be the teller's undecayed version", seen)
	}
}

func TestTransform(t *testing.T) {
	newExp := knowledge.Tick(77)
	tests := []struct {
		name      string
		transform func(knowledge.Fact) knowledge.Fact
		check     func(t *testing.T, f knowledge.Fact, ok bool)
	}{
		{"can change confidence, payload and expiry",
			func(f knowledge.Fact) knowledge.Fact {
				f.Confidence, f.Payload, f.Expiry = 0.6, "twisted", &newExp
				return f
			},
			func(t *testing.T, f knowledge.Fact, ok bool) {
				if !ok || f.Confidence != 0.6 || f.Payload != "twisted" || f.Expiry == nil || *f.Expiry != 77 {
					t.Errorf("got %+v", f)
				}
			}},
		{"cannot change identity or origin",
			func(f knowledge.Fact) knowledge.Fact {
				f.ID, f.Type, f.Subject, f.Discriminator = "other", "other", "other", "other"
				f.Source, f.Observed, f.Private = "observed", 999, true
				f.Prov = knowledge.Provenance{From: "forged", Hops: 0}
				return f
			},
			func(t *testing.T, f knowledge.Fact, ok bool) {
				if !ok || f.Type != "t" || f.Subject != "s" || f.Source != "relayed" || f.Observed != 5 || f.Private || f.Prov.From != "a" || f.Prov.Hops != 1 {
					t.Errorf("got %+v", f)
				}
			}},
		{"confidence is clamped to 1",
			func(f knowledge.Fact) knowledge.Fact { f.Confidence = 7; return f },
			func(t *testing.T, f knowledge.Fact, ok bool) {
				if !ok || f.Confidence != 1 {
					t.Errorf("got %+v", f)
				}
			}},
		{"below the minimum drops the delivery",
			func(f knowledge.Fact) knowledge.Fact { f.Confidence = 0.1; return f },
			func(t *testing.T, f knowledge.Fact, ok bool) {
				if ok {
					t.Errorf("delivered %+v", f)
				}
			}},
		{"NaN drops the delivery",
			func(f knowledge.Fact) knowledge.Fact { f.Confidence = math.NaN(); return f },
			func(t *testing.T, f knowledge.Fact, ok bool) {
				if ok {
					t.Errorf("delivered %+v", f)
				}
			}},
		{"nil transform is the identity",
			nil,
			func(t *testing.T, f knowledge.Fact, ok bool) {
				if !ok || math.Abs(f.Confidence-0.72) > 1e-9 {
					t.Errorf("got %+v", f)
				}
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
			give(t, a, "f", 0.9)
			sp := mustSpreader(t, spread.DefaultConfig())
			sp.Transform = tc.transform
			sp.Spread(a, holders(b), 5)
			f, ok := got(t, b, "f")
			tc.check(t, f, ok)
		})
	}
}

func TestTransformSeesDecayedCopy(t *testing.T) {
	a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
	give(t, a, "f", 0.9)
	var seen knowledge.Fact
	sp := mustSpreader(t, spread.DefaultConfig())
	sp.Transform = func(f knowledge.Fact) knowledge.Fact { seen = f; return f }
	sp.Spread(a, holders(b), 5)
	if math.Abs(seen.Confidence-0.72) > 1e-9 || seen.Source != "relayed" || seen.Prov.From != "a" || seen.Prov.Hops != 1 {
		t.Errorf("transform saw %+v; decay, source and provenance should already be applied", seen)
	}
}

func TestListenerSelection(t *testing.T) {
	a := newPerson("a", kcfg())
	give(t, a, "f", 0.9)
	b := newPerson("b", kcfg())
	noStore := &person{id: "no-store"}
	var nilHolder spread.Holder
	sp := mustSpreader(t, spread.DefaultConfig())
	res, err := sp.Spread(a, []spread.Holder{nilHolder, a, noStore, b}, 0)
	if err != nil || len(res.Transfers) != 1 || res.Transfers[0].To != "b" {
		t.Errorf("transfers = %+v err %v; nil listeners, the teller itself and holders without a store are skipped", res.Transfers, err)
	}
	// A teller without a store, or no teller at all, spreads nothing.
	for _, tl := range []spread.Holder{noStore, nil} {
		if res, err := sp.Spread(tl, holders(b), 0); err != nil || len(res.Transfers) != 0 {
			t.Errorf("teller %v: %+v %v", tl, res, err)
		}
	}
}

func TestMergeDecidesWhatTheListenerKeeps(t *testing.T) {
	a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
	give(t, a, "f", 0.9)
	give(t, b, "f", 0.95) // b saw it first-hand: rank 3 beats the relayed copy (rank 2)
	res, _ := mustSpreader(t, spread.DefaultConfig()).Spread(a, holders(b), 0)
	if len(res.Transfers) != 1 || res.Transfers[0].Outcome != knowledge.Rejected {
		t.Fatalf("transfers = %+v, want one rejected", res.Transfers)
	}
	if f, _ := got(t, b, "f"); f.Source != "observed" || f.Confidence != 0.95 {
		t.Errorf("listener's better knowledge was overwritten: %+v", f)
	}
}

func TestChainDecayMatchesTheSpec(t *testing.T) {
	// 0.9 -> 0.72 -> 0.576 -> 0.4608 -> 0.3686 -> 0.2949; the fifth holder cannot retell (below 0.3).
	sp := mustSpreader(t, spread.DefaultConfig())
	chain := make([]*person, 7)
	for i := range chain {
		chain[i] = newPerson(fmt.Sprint("p", i), kcfg())
	}
	give(t, chain[0], "f", 0.9)
	want := []float64{0.72, 0.576, 0.4608, 0.36864, 0.294912}
	for i := 0; i+1 < len(chain); i++ {
		res, _ := sp.Spread(chain[i], holders(chain[i+1]), 0)
		if i < len(want) {
			if len(res.Transfers) != 1 || math.Abs(res.Transfers[0].Confidence-want[i]) > 1e-9 {
				t.Fatalf("hop %d: %+v, want %v", i+1, res.Transfers, want[i])
			}
		} else if len(res.Transfers) != 0 {
			t.Fatalf("hop %d should not happen: %+v", i+1, res.Transfers)
		}
	}
}

func TestOfferOrderAndDeterminism(t *testing.T) {
	build := func() []spread.Transfer {
		a := newPerson("a", kcfg())
		for _, f := range []struct {
			id string
			c  float64
		}{{"z", 0.5}, {"b", 0.9}, {"a", 0.9}, {"m", 0.7}} {
			give(t, a, f.id, f.c)
		}
		x, y := newPerson("x", kcfg()), newPerson("y", kcfg())
		res, err := mustSpreader(t, spread.DefaultConfig()).Spread(a, holders(y, x), 0)
		if err != nil {
			t.Fatal(err)
		}
		return res.Transfers
	}
	first := build()
	var ids []string
	for _, tr := range first {
		ids = append(ids, tr.To+":"+tr.FactID)
	}
	want := []string{"y:a", "y:b", "y:m", "y:z", "x:a", "x:b", "x:m", "x:z"} // listeners in given order; confidence desc, then ID
	if !slices.Equal(ids, want) {
		t.Errorf("order %v, want %v", ids, want)
	}
	for range 20 {
		if again := build(); !reflect.DeepEqual(first, again) {
			t.Fatalf("output differs between identical runs:\n%v\n%v", first, again)
		}
	}
}

func TestStoreErrorsAreReported(t *testing.T) {
	a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
	give(t, a, "f", 0.9)
	b.kb.Add(knowledge.Fact{ID: "f", Type: "different", Subject: "s", Confidence: 0.1, Source: "observed"})
	res, err := mustSpreader(t, spread.DefaultConfig()).Spread(a, holders(b), 0)
	if !errors.Is(err, knowledge.ErrIdentityMismatch) || len(res.Transfers) != 0 {
		t.Errorf("err = %v transfers = %+v", err, res.Transfers)
	}
}

func ptr[T any](v T) *T { return &v }

// A ring of three holders passing a rumour around settles: after the first lap
// nothing changes any more, because a relayed copy never beats the original.
func TestRingSettles(t *testing.T) {
	cfg := spread.DefaultConfig()
	cfg.SkipBackToSource = false // worst case: nobody is spared their own story
	sp := mustSpreader(t, cfg)
	ring := []*person{newPerson("a", kcfg()), newPerson("b", kcfg()), newPerson("c", kcfg())}
	give(t, ring[0], "f", 0.9)

	snapshot := func() []knowledge.Fact {
		var all []knowledge.Fact
		for _, p := range ring {
			all = append(all, p.kb.All()...)
		}
		return all
	}
	var settled []knowledge.Fact
	for lap := range 6 {
		for i := range ring {
			sp.Spread(ring[i], holders(ring[(i+1)%3]), knowledge.Tick(lap*3+i))
		}
		if lap == 2 {
			settled = snapshot()
		}
	}
	if !reflect.DeepEqual(settled, snapshot()) {
		t.Errorf("ring still changing after three laps:\n%v\n%v", settled, snapshot())
	}
	if f, _ := got(t, ring[0], "f"); f.Source != "observed" || f.Confidence != 0.9 {
		t.Errorf("the original was overwritten by its own echo: %+v", f)
	}
}
