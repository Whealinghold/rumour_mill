package rumourprom_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/rules"
	"github.com/Whealinghold/rumour_mill/spread"

	rumourprom "github.com/Whealinghold/rumour_mill/contrib/prometheus"
)

type person struct {
	id string
	kb *knowledge.Store
}

func (p *person) ID() string                  { return p.id }
func (p *person) Knowledge() *knowledge.Store { return p.kb }

type stranger struct{ person }

func newPerson(t *testing.T, id string, cfg knowledge.Config) *person {
	t.Helper()
	kb, err := knowledge.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &person{id: id, kb: kb}
}

// value returns the counter named name whose label label has value val ("" label
// means the unlabelled counter), or -1 if no such series exists.
func value(t *testing.T, reg *prometheus.Registry, name, label, val string) float64 {
	if t != nil {
		t.Helper()
	}
	fams, err := reg.Gather()
	if err != nil {
		panic(err)
	}
	sum, found := 0.0, false // sums series that differ only in other labels (e.g. source)
	for _, f := range fams {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			v := m.GetCounter().GetValue() + m.GetGauge().GetValue()
			if label == "" {
				return v
			}
			for _, l := range m.GetLabel() {
				if l.GetName() == label && l.GetValue() == val {
					sum, found = sum+v, true
				}
			}
		}
	}
	if found {
		return sum
	}
	return -1
}

// want asserts a set of counter values; key is "name" or "name|label|value".
func want(t *testing.T, reg *prometheus.Registry, exp map[string]float64) {
	t.Helper()
	for k, w := range exp {
		parts := strings.Split(k, "|")
		var label, val string
		if len(parts) == 3 {
			label, val = parts[1], parts[2]
		}
		if got := value(t, reg, parts[0], label, val); got != w {
			t.Errorf("%s = %v, want %v", k, got, w)
		}
	}
}

func newMetrics(t *testing.T) (*prometheus.Registry, *rumourprom.Metrics) {
	t.Helper()
	reg := prometheus.NewRegistry()
	m, err := rumourprom.New(reg)
	if err != nil {
		t.Fatal(err)
	}
	return reg, m
}

func fact(id string) knowledge.Fact {
	return knowledge.Fact{ID: id, Type: "notice", Subject: "s", Confidence: 0.9, Source: "observed"}
}

func TestSpreadCountsDeliveriesMismatchesAndErrors(t *testing.T) {
	reg, m := newMetrics(t)
	if _, err := rumourprom.New(reg); err == nil {
		t.Error("second New on the same registry should fail")
	}
	cfg := knowledge.DefaultConfig()
	ana, bo, cy := newPerson(t, "ana", cfg), newPerson(t, "bo", cfg), newPerson(t, "cy", cfg)
	ana.kb.Add(fact("x"))
	onlyBo := spread.Typed(func(e spread.TypedEncounter[*person]) bool { return e.Listener.id == "bo" })
	sp, err := spread.New(spread.DefaultConfig(), m.Spread(), spread.WithFilter(onlyBo))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Spread(ana, []spread.Holder{bo, cy}, 1); err != nil {
		t.Fatal(err)
	}
	// A *stranger is not a *person: the Typed filter blocks it and counts it.
	if _, err := sp.Spread(ana, []spread.Holder{&stranger{*bo}}, 2); err != nil {
		t.Fatal(err)
	}
	want(t, reg, map[string]float64{
		"rumour_spread_offered_total":                     3,
		"rumour_spread_deliveries_total|outcome|inserted": 1,
		"rumour_spread_dropped_total|reason|filtered":     2,
		"rumour_holder_type_mismatch_total":               1,
		"rumour_spread_errors_total":                      0,
	})

	// Same ID, different subject: the listener's store refuses the copy.
	dee := newPerson(t, "dee", cfg)
	dee.kb.Add(knowledge.Fact{ID: "x", Type: "notice", Subject: "other", Confidence: 0.9, Source: "observed"})
	open, _ := spread.New(spread.DefaultConfig(), m.Spread())
	if _, err := open.Spread(ana, []spread.Holder{dee}, 3); err == nil {
		t.Fatal("want a store error")
	}
	want(t, reg, map[string]float64{"rumour_spread_errors_total": 1, "rumour_spread_offered_total": 4})
}

func TestStoreCountsAddsFromEveryCallerAndEvictions(t *testing.T) {
	reg, m := newMetrics(t)
	cfg := knowledge.DefaultConfig()
	cfg.MaxFacts = 1
	kb, err := knowledge.NewStore(cfg, m.Store())
	if err != nil {
		t.Fatal(err)
	}
	weak := fact("a")
	weak.Confidence = 0.3
	kb.Add(weak)
	kb.Add(fact("b")) // stronger: evicts a
	kb.Add(fact("b")) // identical re-add: rejected
	// A spread delivery into the instrumented store is counted too.
	kb2, _ := knowledge.NewStore(knowledge.DefaultConfig(), m.Store())
	ana := newPerson(t, "ana", knowledge.DefaultConfig())
	ana.kb.Add(fact("c"))
	sp, _ := spread.New(spread.DefaultConfig())
	if _, err := sp.Spread(ana, []spread.Holder{&person{id: "bo", kb: kb2}}, 1); err != nil {
		t.Fatal(err)
	}
	// An add the store refuses is not counted.
	kb.Add(knowledge.Fact{})
	want(t, reg, map[string]float64{
		"rumour_fact_adds_total|outcome|inserted": 3,
		"rumour_fact_adds_total|outcome|rejected": 1,
		"rumour_fact_evictions_total":             1,
	})
}

func TestFadeCounts(t *testing.T) {
	reg, m := newMetrics(t)
	fader := func(f knowledge.Fact, _, _ knowledge.Tick) (float64, bool) {
		switch f.ID {
		case "gone":
			return 0, true
		case "bad":
			return math.NaN(), false
		}
		return 0.5, false
	}
	kb, err := knowledge.NewStore(knowledge.DefaultConfig(), knowledge.WithFader(fader), m.Store())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"keep", "gone", "bad"} {
		kb.Add(fact(id))
	}
	kb.Fade(10)
	want(t, reg, map[string]float64{
		"rumour_fade_examined_total":  3,
		"rumour_fade_changed_total":   1,
		"rumour_fade_forgotten_total": 1,
		"rumour_fade_invalid_total":   1,
	})
	// No fader: Fade does nothing and counts nothing new.
	plain, _ := knowledge.NewStore(knowledge.DefaultConfig(), m.Store())
	plain.Add(fact("z"))
	plain.Fade(10)
	want(t, reg, map[string]float64{"rumour_fade_examined_total": 3})
}

func TestInferCounts(t *testing.T) {
	reg, m := newMetrics(t)
	cfg := infer.DefaultConfig()
	cfg.MaxPasses = 3
	rs := []infer.Rule{
		{ID: "r1", Requires: []infer.Requirement{{Type: "a"}}, Produces: infer.Product{Type: "b"}, Confidence: 0.5},
		{ID: "r2", Requires: []infer.Requirement{{Type: "b"}}, Produces: infer.Product{Type: "c"}, Confidence: 0.5},
	}
	eng, err := infer.New(cfg, rs, m.Infer())
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := knowledge.NewStore(knowledge.DefaultConfig(), m.Store())
	kb.Add(knowledge.Fact{ID: "f", Type: "a", Subject: "s", Confidence: 0.9, Source: "observed"})
	res, err := eng.Run(kb, 1)
	if err != nil || len(res.Derived) != 2 {
		t.Fatalf("derived %d, err %v", len(res.Derived), err)
	}
	if _, err := eng.Run(nil, 2); err == nil {
		t.Fatal("want ErrNilStore")
	}
	want(t, reg, map[string]float64{
		"rumour_infer_runs_total":                     2,
		"rumour_infer_passes_total":                   3,
		"rumour_infer_derived_total|outcome|inserted": 2,
		"rumour_infer_errors_total":                   1,
		// the derived facts also went through the store hook
		"rumour_fact_adds_total|outcome|inserted": 3,
	})
}

type rctx struct{}

func TestRulesCounts(t *testing.T) {
	reg, m := newMetrics(t)
	r, err := rules.NewRegistry(rules.DefaultConfig(), rules.Layer[rctx]{Name: "city"}, rules.Layer[rctx]{Name: "home"})
	if err != nil {
		t.Fatal(err)
	}
	r.Observe(m.Rules())
	boom := func(rctx) error { return errors.New("boom") }
	for _, rl := range []struct {
		layer string
		rule  rules.Rule[rctx]
	}{
		{"city", rules.Rule[rctx]{ID: "a", Event: "ev1", Stop: rules.StopLayer}},
		{"city", rules.Rule[rctx]{ID: "b", Event: "ev1", Priority: 1}}, // cut off by a
		{"home", rules.Rule[rctx]{ID: "c", Event: "ev1", Do: boom}},
		{"city", rules.Rule[rctx]{ID: "d", Event: "ev2", Stop: rules.StopAll}},
		{"home", rules.Rule[rctx]{ID: "e", Event: "ev2"}}, // cut off by d
	} {
		if err := r.Register(rl.layer, "", rl.rule); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Process(rctx{}, "ev1"); err == nil {
		t.Fatal("want action error")
	}
	if _, err := r.Process(rctx{}, "ev2"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Process(rctx{}, "none"); err != nil {
		t.Fatal(err)
	}
	want(t, reg, map[string]float64{
		"rumour_rules_processed_total":          3,
		"rumour_rules_fired_total|layer|city":   2,
		"rumour_rules_fired_total|layer|home":   1,
		"rumour_rules_stopped_total|mode|layer": 1,
		"rumour_rules_stopped_total|mode|all":   1,
		"rumour_rules_errors_total":             1,
	})
}

func TestMetricsOnDifferentRegistriesDoNotInterfere(t *testing.T) {
	reg1, m1 := newMetrics(t)
	reg2, _ := newMetrics(t)
	cfg := knowledge.DefaultConfig()
	ana, bo := newPerson(t, "ana", cfg), newPerson(t, "bo", cfg)
	ana.kb.Add(fact("x"))
	sp, _ := spread.New(spread.DefaultConfig(), m1.Spread())
	sp.Spread(ana, []spread.Holder{bo}, 1)
	want(t, reg1, map[string]float64{"rumour_spread_offered_total": 1})
	want(t, reg2, map[string]float64{"rumour_spread_offered_total": 0, "rumour_spread_deliveries_total|outcome|inserted": 0})
}

func TestZeroSeriesAfterNew(t *testing.T) {
	reg, _ := newMetrics(t)
	exp := map[string]float64{
		"rumour_rules_stopped_total|mode|layer": 0,
		"rumour_rules_stopped_total|mode|all":   0,
		// layer names are caller-defined, so fired has no series yet
		"rumour_rules_fired_total": -1,
	}
	for _, o := range []knowledge.Outcome{knowledge.Rejected, knowledge.Inserted, knowledge.Replaced} {
		for _, name := range []string{"rumour_spread_deliveries_total", "rumour_fact_adds_total"} {
			exp[name+"|outcome|"+o.String()] = 0
		}
	}
	spread.Dropped{}.Each(func(reason string, _ int) { exp["rumour_spread_dropped_total|reason|"+reason] = 0 })
	spread.Withheld{}.Each(func(reason string, _ int) { exp["rumour_spread_withheld_total|reason|"+reason] = 0 })
	exp["rumour_infer_derived_total|outcome|inserted"] = 0
	exp["rumour_infer_derived_total|outcome|replaced"] = 0
	exp["rumour_infer_derived_total|outcome|rejected"] = -1 // cannot occur, so never seeded
	for _, n := range []string{
		"rumour_spread_offered_total", "rumour_spread_errors_total", "rumour_holder_type_mismatch_total",
		"rumour_fact_evictions_total",
		"rumour_fact_pruned_total", "rumour_fade_examined_total", "rumour_fade_changed_total",
		"rumour_fade_forgotten_total", "rumour_fade_invalid_total", "rumour_infer_runs_total",
		"rumour_infer_passes_total", "rumour_infer_errors_total", "rumour_rules_processed_total",
		"rumour_rules_errors_total",
	} {
		exp[n] = 0
	}
	want(t, reg, exp)
}

func TestNewRollsBackOnConflict(t *testing.T) {
	reg := prometheus.NewRegistry()
	// Pre-register a collector that collides with the last one New registers.
	reg.MustRegister(prometheus.NewCounter(prometheus.CounterOpts{Name: "rumour_rules_stopped_total", Help: "x"}))
	if _, err := rumourprom.New(reg); err == nil {
		t.Fatal("want a registration conflict")
	}
	if got := value(t, reg, "rumour_spread_offered_total", "", ""); got != -1 {
		t.Errorf("partial registration left behind: offered = %v", got)
	}
}

func TestEveryDropReasonCounted(t *testing.T) {
	cfg := knowledge.DefaultConfig()
	fact := knowledge.Fact{ID: "x", Type: "notice", Subject: "s", Confidence: 0.9, Source: "observed"}
	nan := func(_ spread.Encounter, f knowledge.Fact) knowledge.Fact { f.Confidence = math.NaN(); return f }
	lowDecay := spread.DefaultConfig()
	lowDecay.DecayFactor, lowDecay.MinConfidence = 0.5, 0.9

	for _, tc := range []struct {
		reason string
		cfg    spread.Config
		setup  func(sp *spread.Spreader, ana, bo *person)
	}{
		{"filtered", spread.DefaultConfig(), nil},
		{"back_to_source", spread.DefaultConfig(), func(sp *spread.Spreader, ana, bo *person) {
			sp.Spread(ana, []spread.Holder{bo}, 1) // bo now holds a copy told by ana
			sp.Spread(bo, []spread.Holder{ana}, 2) // telling it back to ana is skipped
		}},
		{"below_min_confidence", lowDecay, nil},
		{"transform_dropped", spread.DefaultConfig(), nil},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			m, _ := rumourprom.New(reg)
			ana, bo := newPerson(t, "ana", cfg), newPerson(t, "bo", cfg)
			ana.kb.Add(fact)
			opts := []spread.Option{m.Spread()}
			if tc.reason == "filtered" {
				opts = append(opts, spread.WithFilter(func(spread.Encounter) bool { return false }))
			}
			sp, _ := spread.New(tc.cfg, opts...)
			if tc.reason == "transform_dropped" {
				sp.Transform = nan
			}
			if tc.setup != nil {
				tc.setup(sp, ana, bo)
			} else {
				sp.Spread(ana, []spread.Holder{bo}, 1)
			}
			if got := value(t, reg, "rumour_spread_dropped_total", "reason", tc.reason); got != 1 {
				t.Errorf("dropped{reason=%q} = %v, want 1", tc.reason, got)
			}
		})
	}
}

func TestPruneCounts(t *testing.T) {
	reg, m := newMetrics(t)
	kb, err := knowledge.NewStore(knowledge.DefaultConfig(), m.Store())
	if err != nil {
		t.Fatal(err)
	}
	exp := knowledge.Tick(5)
	for _, id := range []string{"a", "b"} {
		f := fact(id)
		f.Expiry = &exp
		kb.Add(f)
	}
	kb.Add(fact("forever"))
	if n := kb.Prune(1); n != 0 { // nothing expired: still a call, adds 0
		t.Fatalf("pruned %d early", n)
	}
	if n := kb.Prune(5); n != 2 {
		t.Fatalf("pruned %d, want 2", n)
	}
	want(t, reg, map[string]float64{"rumour_fact_pruned_total": 2})
}

func TestWithheldCounts(t *testing.T) {
	reg, m := newMetrics(t)
	cfg := knowledge.DefaultConfig()
	ana, bo, cy := newPerson(t, "ana", cfg), newPerson(t, "bo", cfg), newPerson(t, "cy", cfg)
	ana.kb.Add(fact("open"))
	priv := fact("priv")
	priv.Private = true
	ana.kb.Add(priv)
	far := fact("far")
	far.Prov.Hops = 2
	ana.kb.Add(far)
	sc := spread.DefaultConfig()
	sc.MaxHops = 2
	sp, err := spread.New(sc, m.Spread())
	if err != nil {
		t.Fatal(err)
	}
	// Two listeners: withheld counts facts, not fact-listener pairs.
	if _, err := sp.Spread(ana, []spread.Holder{bo, cy}, 1); err != nil {
		t.Fatal(err)
	}
	want(t, reg, map[string]float64{
		"rumour_spread_withheld_total|reason|private":  1,
		"rumour_spread_withheld_total|reason|max_hops": 1,
		"rumour_spread_offered_total":                  2, // withheld is not offered
	})
}

func TestHelpTextIsPinned(t *testing.T) {
	reg := prometheus.NewRegistry()
	if _, err := rumourprom.New(reg); err != nil {
		t.Fatal(err)
	}
	fams, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"rumour_fact_adds_total":       "Store adds that returned no error, by outcome and source kind (a rejected add is still an add).",
		"rumour_spread_withheld_total": "Facts the teller held that were not offered at all, by reason (not part of offered or dropped).",
	}
	for _, f := range fams {
		if w, ok := want[f.GetName()]; ok {
			if f.GetHelp() != w {
				t.Errorf("%s help = %q, want %q", f.GetName(), f.GetHelp(), w)
			}
			delete(want, f.GetName())
		}
	}
	if len(want) != 0 {
		t.Errorf("metrics not exported: %v", want)
	}
}
