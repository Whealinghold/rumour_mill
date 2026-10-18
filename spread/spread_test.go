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
	sp.Transform = func(_ spread.Encounter, f knowledge.Fact) knowledge.Fact { f.Private = false; return f }
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
		transform spread.Transform
		check     func(t *testing.T, f knowledge.Fact, ok bool)
	}{
		{"can change confidence, payload and expiry",
			func(_ spread.Encounter, f knowledge.Fact) knowledge.Fact {
				f.Confidence, f.Payload, f.Expiry = 0.6, "twisted", &newExp
				return f
			},
			func(t *testing.T, f knowledge.Fact, ok bool) {
				if !ok || f.Confidence != 0.6 || f.Payload != "twisted" || f.Expiry == nil || *f.Expiry != 77 {
					t.Errorf("got %+v", f)
				}
			}},
		{"cannot change identity or origin",
			func(_ spread.Encounter, f knowledge.Fact) knowledge.Fact {
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
			func(_ spread.Encounter, f knowledge.Fact) knowledge.Fact { f.Confidence = 7; return f },
			func(t *testing.T, f knowledge.Fact, ok bool) {
				if !ok || f.Confidence != 1 {
					t.Errorf("got %+v", f)
				}
			}},
		{"below the minimum drops the delivery",
			func(_ spread.Encounter, f knowledge.Fact) knowledge.Fact { f.Confidence = 0.1; return f },
			func(t *testing.T, f knowledge.Fact, ok bool) {
				if ok {
					t.Errorf("delivered %+v", f)
				}
			}},
		{"NaN drops the delivery",
			func(_ spread.Encounter, f knowledge.Fact) knowledge.Fact { f.Confidence = math.NaN(); return f },
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
	sp.Transform = func(_ spread.Encounter, f knowledge.Fact) knowledge.Fact { seen = f; return f }
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

// ctxFact is a fact observed at tick 0, held by every teller in the S1-S5 tests.
func ctxFact(t *testing.T, p *person) {
	t.Helper()
	if _, err := p.kb.Add(knowledge.Fact{ID: "cargo_late", Type: "t", Subject: "s", Confidence: 0.8, Source: "observed"}); err != nil {
		t.Fatal(err)
	}
}

// S1-S3: one hook, behaviour depends on teller, listener and age.
func TestTransformUsesContext(t *testing.T) {
	sp := func(tr spread.Transform) *spread.Spreader {
		s := mustSpreader(t, spread.DefaultConfig())
		s.Transform = tr
		return s
	}
	tests := []struct {
		name   string
		tr     spread.Transform
		teller string
		to     string
		now    knowledge.Tick
		want   float64
	}{
		{"boastful teller inflates", func(e spread.Encounter, d knowledge.Fact) knowledge.Fact {
			if e.Teller.ID() == "gossip-bram" {
				d.Confidence += 0.1
			}
			return d
		}, "gossip-bram", "neutral", 40, 0.74},
		{"other teller unchanged", func(e spread.Encounter, d knowledge.Fact) knowledge.Fact {
			if e.Teller.ID() == "gossip-bram" {
				d.Confidence += 0.1
			}
			return d
		}, "trader", "neutral", 40, 0.64},
		{"sceptical listener halves", func(e spread.Encounter, d knowledge.Fact) knowledge.Fact {
			if e.Listener.ID() == "sceptic-ola" {
				d.Confidence /= 2
			}
			return d
		}, "trader", "sceptic-ola", 40, 0.32},
		{"neutral listener unchanged", func(e spread.Encounter, d knowledge.Fact) knowledge.Fact {
			if e.Listener.ID() == "sceptic-ola" {
				d.Confidence /= 2
			}
			return d
		}, "trader", "neutral", 40, 0.64},
		{"young news unchanged", stale, "trader", "neutral", 40, 0.64},
		{"stale news halved", stale, "trader", "neutral", 90, 0.32},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, b := newPerson(tc.teller, kcfg()), newPerson(tc.to, kcfg())
			ctxFact(t, a)
			sp(tc.tr).Spread(a, holders(b), tc.now)
			f, ok := got(t, b, "cargo_late")
			if !ok || math.Abs(f.Confidence-tc.want) > 1e-9 {
				t.Errorf("got %+v ok=%v, want confidence %v", f, ok, tc.want)
			}
		})
	}
}

func stale(e spread.Encounter, d knowledge.Fact) knowledge.Fact {
	if e.Now-e.Fact.Observed > 50 {
		d.Confidence /= 2
	}
	return d
}

// R2: Encounter.Fact is the teller's undecayed version while delivered is decayed.
func TestTransformEncounterFactIsUndecayed(t *testing.T) {
	a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
	ctxFact(t, a)
	sp := mustSpreader(t, spread.DefaultConfig())
	var enc spread.Encounter
	var del knowledge.Fact
	sp.Transform = func(e spread.Encounter, d knowledge.Fact) knowledge.Fact { enc, del = e, d; return d }
	sp.Spread(a, holders(b), 5)
	if enc.Fact.Confidence != 0.8 || math.Abs(del.Confidence-0.64) > 1e-9 || enc.Fact.Source != "observed" || del.Source != "relayed" {
		t.Errorf("encounter fact %+v, delivered %+v", enc.Fact, del)
	}
}

// S4/S5 (R3-R5): restoration, clamp, NaN, MinConfidence after transform, nil transform.
func TestTransformPipeline(t *testing.T) {
	tests := []struct {
		name   string
		tr     spread.Transform
		want   float64
		wantOK bool
	}{
		{"nil unchanged", nil, 0.64, true},
		{"identity fields restored", func(_ spread.Encounter, d knowledge.Fact) knowledge.Fact {
			d.Type, d.Source, d.Prov = "x", "observed", knowledge.Provenance{From: "forged"}
			return d
		}, 0.64, true},
		{"1.7 clamped", func(_ spread.Encounter, d knowledge.Fact) knowledge.Fact { d.Confidence = 1.7; return d }, 1, true},
		{"NaN dropped", func(_ spread.Encounter, d knowledge.Fact) knowledge.Fact { d.Confidence = math.NaN(); return d }, 0, false},
		{"below MinConfidence skipped", func(_ spread.Encounter, d knowledge.Fact) knowledge.Fact { d.Confidence = 0.15; return d }, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
			ctxFact(t, a)
			sp := mustSpreader(t, spread.DefaultConfig())
			sp.Transform = tc.tr
			sp.Spread(a, holders(b), 5)
			f, ok := got(t, b, "cargo_late")
			if ok != tc.wantOK {
				t.Fatalf("delivered=%v, want %v (%+v)", ok, tc.wantOK, f)
			}
			if ok && (math.Abs(f.Confidence-tc.want) > 1e-9 || f.Type != "t" || f.Source != "relayed" || f.Prov.From != "a" || f.Prov.Hops != 1) {
				t.Errorf("got %+v", f)
			}
		})
	}
}

// R1: the Encounter handed to Transform equals the one the filters judged.
func TestTransformGetsTheFilterEncounter(t *testing.T) {
	a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
	ctxFact(t, a)
	var fromFilter, fromTransform spread.Encounter
	sp := mustSpreader(t, spread.DefaultConfig(), spread.WithFilter(func(e spread.Encounter) bool { fromFilter = e; return true }))
	sp.Transform = func(e spread.Encounter, d knowledge.Fact) knowledge.Fact { fromTransform = e; return d }
	sp.Spread(a, holders(b), 9)
	if !reflect.DeepEqual(fromFilter, fromTransform) || fromTransform.Now != 9 {
		t.Errorf("filter saw %+v, transform saw %+v", fromFilter, fromTransform)
	}
}

// R7: the teller's store is untouched by a spread with a transform.
func TestTransformLeavesTellerUntouched(t *testing.T) {
	a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
	ctxFact(t, a)
	a.kb.Add(knowledge.Fact{ID: "p", Type: "t", Subject: "p", Confidence: 0.8, Source: "derived", Prov: knowledge.Provenance{Premises: []string{"q", "r"}}})
	before := a.kb.All()
	sp := mustSpreader(t, spread.DefaultConfig())
	sp.Transform = func(_ spread.Encounter, d knowledge.Fact) knowledge.Fact { d.Confidence = 0.5; return d }
	sp.Spread(a, holders(b), 5)
	if !reflect.DeepEqual(before, a.kb.All()) {
		t.Errorf("teller store changed: %+v -> %+v", before, a.kb.All())
	}
	if p, _ := a.kb.Get("p"); !reflect.DeepEqual(p.Prov.Premises, []string{"q", "r"}) {
		t.Errorf("teller premises changed: %v", p.Prov.Premises)
	}
}

func TestDeliveredResetsFadedAt(t *testing.T) {
	a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
	a.kb.Add(knowledge.Fact{ID: "f", Type: "t", Source: "observed", Confidence: 0.9, Observed: 1, FadedAt: 200})
	sp := mustSpreader(t, spread.DefaultConfig())
	sp.Spread(a, holders(b), 300)
	got, ok := b.kb.Get("f")
	if !ok || got.FadedAt != 0 {
		t.Errorf("delivered fact = %+v (found %v), want FadedAt 0", got, ok)
	}
	if tf, _ := a.kb.Get("f"); tf.FadedAt != 200 {
		t.Errorf("teller FadedAt = %d, want 200", tf.FadedAt)
	}
}

// R3: Transform runs once per delivery that passed the filters, never for
// filter-rejected encounters.
func TestTransformCallCount(t *testing.T) {
	tests := []struct {
		name      string
		listeners []string
		facts     int
		reject    bool
		want      int
	}{
		{"filter rejects every encounter", []string{"b"}, 2, true, 0},
		{"one listener, one fact", []string{"b"}, 1, false, 1},
		{"two listeners, two facts", []string{"b", "c"}, 2, false, 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newPerson("a", kcfg())
			for i := range tc.facts {
				give(t, a, fmt.Sprintf("f%d", i), 0.9)
			}
			var ls []*person
			for _, id := range tc.listeners {
				ls = append(ls, newPerson(id, kcfg()))
			}
			sp := mustSpreader(t, spread.DefaultConfig(), spread.WithFilter(func(spread.Encounter) bool { return !tc.reject }))
			calls := 0
			sp.Transform = func(_ spread.Encounter, d knowledge.Fact) knowledge.Fact { calls++; return d }
			res, err := sp.Spread(a, holders(ls...), 5)
			if err != nil {
				t.Fatal(err)
			}
			if calls != tc.want || len(res.Transfers) != tc.want {
				t.Errorf("transform calls = %d, transfers = %d, want %d", calls, len(res.Transfers), tc.want)
			}
		})
	}
}

// R3: the MinConfidence check comes after Transform, so a transform can lift a
// copy that decay pushed below MinConfidence (0.3 * 0.5 = 0.15 < 0.2), and
// can equally push a healthy copy below it.
func TestTransformRunsBeforeMinConfidenceCheck(t *testing.T) {
	tests := []struct {
		name   string
		source float64
		lift   float64
		wantOK bool
	}{
		{"decayed below minimum, lifted above: delivered", 0.3, 0.5, true},
		{"decayed below minimum, not lifted: skipped", 0.3, 0.15, false},
		{"healthy, pushed below minimum: skipped", 0.9, 0.1, false},
		{"lifted exactly to the minimum: delivered", 0.3, 0.2, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := spread.DefaultConfig()
			cfg.DecayFactor = 0.5
			a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
			give(t, a, "f", tc.source)
			sp := mustSpreader(t, cfg)
			sp.Transform = func(_ spread.Encounter, d knowledge.Fact) knowledge.Fact { d.Confidence = tc.lift; return d }
			sp.Spread(a, holders(b), 5)
			f, ok := got(t, b, "f")
			if ok != tc.wantOK || (ok && f.Confidence != tc.lift) {
				t.Errorf("delivered = %v (%+v), want %v at %v", ok, f, tc.wantOK, tc.lift)
			}
		})
	}
}

// R4/P-D3: Discriminator and FadedAt are outside the transform's reach.
func TestTransformCannotChangeDiscriminatorOrFadedAt(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*knowledge.Fact)
	}{
		{"discriminator", func(f *knowledge.Fact) { f.Discriminator = "forged" }},
		{"faded at", func(f *knowledge.Fact) { f.FadedAt = 999 }},
		{"both", func(f *knowledge.Fact) { f.Discriminator, f.FadedAt = "forged", 999 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
			a.kb.Add(knowledge.Fact{ID: "f", Type: "t", Subject: "s", Discriminator: "d", Confidence: 0.9, Source: "observed", FadedAt: 200})
			sp := mustSpreader(t, spread.DefaultConfig())
			sp.Transform = func(_ spread.Encounter, d knowledge.Fact) knowledge.Fact { tc.mutate(&d); return d }
			sp.Spread(a, holders(b), 300)
			f, ok := got(t, b, "f")
			if !ok || f.Discriminator != "d" || f.FadedAt != 0 {
				t.Errorf("listener copy = %+v (found %v), want Discriminator d, FadedAt 0", f, ok)
			}
		})
	}
}

// R4 at Result level: the reported transfer confidence is clamped, and a NaN
// transform silently drops the delivery without an error.
func TestTransformResultClampAndNaN(t *testing.T) {
	run := func(c float64) (spread.Result, error) {
		a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
		ctxFact(t, a)
		sp := mustSpreader(t, spread.DefaultConfig())
		sp.Transform = func(_ spread.Encounter, d knowledge.Fact) knowledge.Fact { d.Confidence = c; return d }
		return sp.Spread(a, holders(b), 5)
	}
	res, err := run(1.7)
	if err != nil || len(res.Transfers) != 1 || res.Transfers[0].Confidence != 1 {
		t.Errorf("clamp: res %+v, err %v", res, err)
	}
	res, err = run(math.NaN())
	if err != nil {
		t.Errorf("NaN: err = %v, want nil", err)
	}
	if len(res.Transfers) != 0 {
		t.Errorf("NaN: transfers = %+v, want none", res.Transfers)
	}
}

// Result.Offered and Result.Dropped explain every offer that did not arrive.
func TestResultCountsDropReasons(t *testing.T) {
	deny := func(spread.Encounter) bool { return false }
	nan := func(_ spread.Encounter, d knowledge.Fact) knowledge.Fact { d.Confidence = math.NaN(); return d }
	for _, tc := range []struct {
		name     string
		cfg      func(*spread.Config)
		opts     []spread.Option
		tr       spread.Transform
		backward bool // spread from the fact's source back to the teller
		want     spread.Dropped
		offered  int
		moved    int
	}{
		{name: "delivered", offered: 1, moved: 1},
		{name: "filtered", opts: []spread.Option{spread.WithFilter(deny)}, offered: 1, want: spread.Dropped{Filtered: 1}},
		{name: "back to source", backward: true, offered: 1, want: spread.Dropped{BackToSource: 1}},
		{name: "below min confidence", cfg: func(c *spread.Config) { c.MinConfidence = 0.95 }, offered: 1, want: spread.Dropped{BelowMinConfidence: 1}},
		{name: "transform NaN", tr: nan, offered: 1, want: spread.Dropped{TransformDropped: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := spread.DefaultConfig()
			if tc.cfg != nil {
				tc.cfg(&cfg)
			}
			sp := mustSpreader(t, cfg, tc.opts...)
			sp.Transform = tc.tr
			a, b := newPerson("a", kcfg()), newPerson("b", kcfg())
			give(t, a, "f", 0.9)
			teller, listener := a, b
			if tc.backward {
				if _, err := b.kb.Add(knowledge.Fact{ID: "f", Type: "t", Subject: "s", Confidence: 0.9, Source: "heard", Prov: knowledge.Provenance{From: "a", Hops: 1}}); err != nil {
					t.Fatal(err)
				}
				teller, listener = b, a
			}
			res, err := sp.Spread(teller, holders(listener), 0)
			if err != nil {
				t.Fatal(err)
			}
			if res.Offered != tc.offered || res.Dropped != tc.want || len(res.Transfers) != tc.moved {
				t.Errorf("offered=%d dropped=%+v transfers=%d, want %d %+v %d",
					res.Offered, res.Dropped, len(res.Transfers), tc.offered, tc.want, tc.moved)
			}
		})
	}
}

// Integration: fading and spreading share one fact's life. Spread tells the
// teller's faded confidence, and the listener's copy fades from its own arrival
// tick, not from the original observation.
func TestSpreadAfterFadeThenListenerFadesFromArrival(t *testing.T) {
	sc := spread.DefaultConfig()
	kc := kcfg()
	kc.Fade.HalfLives = map[string]int64{"observed": 100, sc.ReceivedKind: 100}
	a, b := newPerson("a", kc), newPerson("b", kc)
	a.kb.Add(knowledge.Fact{ID: "f", Type: "t", Source: "observed", Confidence: 0.8, Observed: 0})
	sp := mustSpreader(t, sc)

	a.kb.Fade(100) // 0.8 -> 0.4
	sp.Spread(a, holders(b), 100)
	got, ok := b.kb.Get("f")
	if want := 0.4 * sc.DecayFactor; !ok || math.Abs(got.Confidence-want) > 1e-9 {
		t.Fatalf("delivered = %+v (found %v), want confidence %v", got, ok, want)
	}
	b.kb.Fade(200) // one half-life since arrival at 100, not two since observation at 0
	got, _ = b.kb.Get("f")
	if want := 0.4 * sc.DecayFactor / 2; math.Abs(got.Confidence-want) > 1e-9 {
		t.Errorf("after listener fade = %v, want %v", got.Confidence, want)
	}
}
