package rumourprom_test

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/rules"
	"github.com/Whealinghold/rumour_mill/spread"

	rumourprom "github.com/Whealinghold/rumour_mill/contrib/prometheus"
)

// get returns the value of the counter or gauge (the sample count of a
// histogram) named name whose labels include every "k=v" pair, or -1 if absent.
func get(t *testing.T, reg *prometheus.Registry, name string, labels ...string) float64 {
	t.Helper()
	fams, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fams {
		if f.GetName() != name {
			continue
		}
	next:
		for _, m := range f.GetMetric() {
			for _, kv := range labels {
				k, v, _ := strings.Cut(kv, "=")
				ok := false
				for _, l := range m.GetLabel() {
					ok = ok || (l.GetName() == k && l.GetValue() == v)
				}
				if !ok {
					continue next
				}
			}
			switch {
			case m.GetHistogram() != nil:
				return float64(m.GetHistogram().GetSampleCount())
			case m.GetGauge() != nil:
				return m.GetGauge().GetValue()
			}
			return m.GetCounter().GetValue()
		}
	}
	return -1
}

func histSum(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	fams, _ := reg.Gather()
	for _, f := range fams {
		if f.GetName() == name {
			return f.GetMetric()[0].GetHistogram().GetSampleSum()
		}
	}
	return -1
}

func bounds(t *testing.T, reg *prometheus.Registry, name string) []float64 {
	t.Helper()
	fams, _ := reg.Gather()
	var out []float64
	for _, f := range fams {
		if f.GetName() == name {
			for _, b := range f.GetMetric()[0].GetHistogram().GetBucket() {
				out = append(out, b.GetUpperBound())
			}
		}
	}
	return out
}

// stepClock returns a clock that advances by step on every read.
func stepClock(step time.Duration) func() time.Time {
	t := time.Unix(0, 0)
	return func() time.Time { t = t.Add(step); return t }
}

func expect(t *testing.T, reg *prometheus.Registry, exp map[string]float64) {
	t.Helper()
	for k, w := range exp {
		parts := strings.Split(k, "|")
		if got := get(t, reg, parts[0], parts[1:]...); got != w {
			t.Errorf("%s = %v, want %v", k, got, w)
		}
	}
}

func TestDefaultBuckets(t *testing.T) {
	for name, tc := range map[string]struct {
		got  func() []float64
		want []float64
	}{
		"duration":   {rumourprom.DefaultDurationBuckets, []float64{1e-6, 5e-6, 2.5e-5, 1e-4, 5e-4, 2.5e-3, 1e-2, 5e-2, 0.25, 1}},
		"hops":       {rumourprom.DefaultHopBuckets, []float64{1, 2, 3, 4, 5, 6, 8, 10, 15, 20}},
		"confidence": {rumourprom.DefaultConfidenceBuckets, []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1}},
	} {
		if g := tc.got(); !reflect.DeepEqual(g, tc.want) {
			t.Errorf("%s = %v, want %v", name, g, tc.want)
		}
		a := tc.got()
		a[0] = 99
		if tc.got()[0] == 99 {
			t.Errorf("%s returned a shared slice", name)
		}
	}
	reg, _ := newMetrics(t)
	for name, w := range map[string][]float64{
		"rumour_operation_duration_seconds":  append(rumourprom.DefaultDurationBuckets(), math.Inf(1))[:10],
		"rumour_spread_delivered_hops":       rumourprom.DefaultHopBuckets(),
		"rumour_spread_delivered_confidence": rumourprom.DefaultConfidenceBuckets(),
	} {
		got := bounds(t, reg, name)
		if name == "rumour_operation_duration_seconds" {
			got = got[:10]
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("%s buckets = %v, want %v", name, got, w)
		}
	}
}

func TestBucketOptionsOverrideAndNewErrors(t *testing.T) {
	reg := prometheus.NewRegistry()
	_, err := rumourprom.New(reg, rumourprom.WithHopBuckets(1, 2), rumourprom.WithConfidenceBuckets(0.5, 1), rumourprom.WithDurationBuckets(0.1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := bounds(t, reg, "rumour_spread_delivered_hops"); !reflect.DeepEqual(got, []float64{1, 2}) {
		t.Errorf("hops = %v", got)
	}
	if got := bounds(t, reg, "rumour_spread_delivered_confidence"); !reflect.DeepEqual(got, []float64{0.5, 1}) {
		t.Errorf("confidence = %v", got)
	}
	if got := bounds(t, reg, "rumour_operation_duration_seconds"); !reflect.DeepEqual(got[:2], []float64{0.1, 1}) {
		t.Errorf("duration = %v", got)
	}
	for name, opt := range map[string]rumourprom.Option{
		"empty duration": rumourprom.WithDurationBuckets(),
		"nan hops":       rumourprom.WithHopBuckets(1, math.NaN()),
		"inf confidence": rumourprom.WithConfidenceBuckets(math.Inf(1)),
		"not increasing": rumourprom.WithHopBuckets(2, 2),
		"decreasing":     rumourprom.WithDurationBuckets(2, 1),
		"empty source":   rumourprom.WithSourceKinds("a", ""),
	} {
		r := prometheus.NewRegistry()
		if _, err := rumourprom.New(r, opt); err == nil {
			t.Errorf("%s: want error", name)
		}
		if fams, _ := r.Gather(); len(fams) != 0 {
			t.Errorf("%s: left %d families registered", name, len(fams))
		}
	}
	if _, err := rumourprom.New(prometheus.NewRegistry(), nil); err != nil {
		t.Errorf("nil option should be ignored: %v", err)
	}
}

func TestSourceLabel(t *testing.T) {
	kinds := []string{"seen", "heard", "seen", "other"} // duplicates and "other" accepted
	for _, tc := range []struct {
		name   string
		opts   []rumourprom.Option
		source string
		want   map[string]float64
	}{
		{"no option: all other", nil, "seen", map[string]float64{"inserted|source=other": 1, "inserted|source=seen": -1}},
		{"listed kind verbatim", []rumourprom.Option{rumourprom.WithSourceKinds(kinds...)}, "seen", map[string]float64{"inserted|source=seen": 1, "inserted|source=other": 0, "rejected|source=heard": 0}},
		{"unlisted kind", []rumourprom.Option{rumourprom.WithSourceKinds(kinds...)}, "guess", map[string]float64{"inserted|source=other": 1, "inserted|source=guess": -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			m, err := rumourprom.New(reg, tc.opts...)
			if err != nil {
				t.Fatal(err)
			}
			kb, _ := knowledge.NewStore(knowledge.DefaultConfig(), m.Store())
			f := fact("a")
			f.Source = tc.source
			kb.Add(f)
			for k, w := range tc.want {
				o, s, _ := strings.Cut(k, "|")
				if got := get(t, reg, "rumour_fact_adds_total", "outcome="+o, s); got != w {
					t.Errorf("%s = %v, want %v", k, got, w)
				}
			}
		})
	}
}

func TestStoreNewMetrics(t *testing.T) {
	reg, m := newMetrics(t)
	fader := func(f knowledge.Fact, _, _ knowledge.Tick) (float64, bool) { return f.Confidence, false }
	cfg := knowledge.DefaultConfig()
	kb, err := knowledge.NewStore(cfg, m.Store(), knowledge.WithFader(fader), knowledge.WithClock(stepClock(time.Millisecond)))
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := knowledge.NewStore(cfg, m.Store())
	exp := knowledge.Tick(5)
	for _, id := range []string{"a", "b", "c"} {
		f := fact(id)
		f.Expiry = &exp
		kb.Add(f)
	}
	kb.Add(knowledge.Fact{})                                                                        // invalid
	kb.Add(knowledge.Fact{ID: "a", Type: "notice", Subject: "other", Confidence: 0.9, Source: "x"}) // identity mismatch
	if got := get(t, reg, "rumour_store_size"); got != 3 {
		t.Errorf("size = %v, want 3", got)
	}
	kb.Fade(1)
	plain.Fade(1) // fader-less: still a run
	kb.Prune(1)   // removes nothing, still a run
	kb.Prune(5)   // removes all three
	kb.Find(knowledge.Query{}, 0)
	expect(t, reg, map[string]float64{
		"rumour_fact_add_errors_total|reason=invalid":           1,
		"rumour_fact_add_errors_total|reason=identity_mismatch": 1,
		"rumour_fade_runs_total|fader=wired":                    1,
		"rumour_fade_runs_total|fader=none":                     1,
		"rumour_fade_examined_total":                            3,
		"rumour_prune_runs_total":                               2,
		"rumour_fact_pruned_total":                              3,
		"rumour_store_size":                                     0, // last seen: after the second prune
		"rumour_store_size_max":                                 3,
		"rumour_operation_duration_seconds|op=fade":             1,
		"rumour_operation_duration_seconds|op=prune":            2,
		"rumour_operation_duration_seconds|op=store_find":       1,
		"rumour_operation_duration_seconds|op=spread":           0,
	})
	if s := histSum(t, reg, "rumour_operation_duration_seconds"); s <= 0 {
		t.Errorf("duration sum = %v", s)
	}
}

func TestSpreadNewMetrics(t *testing.T) {
	reg, m := newMetrics(t)
	cfg := knowledge.DefaultConfig()
	ana, bo := newPerson(t, "ana", cfg), newPerson(t, "bo", cfg)
	exp := knowledge.Tick(5)
	for _, f := range []knowledge.Fact{
		fact("open"),
		{ID: "weak", Type: "notice", Subject: "s", Confidence: 0.1, Source: "observed"},
		{ID: "priv", Type: "notice", Subject: "s", Confidence: 0.9, Source: "observed", Private: true},
		{ID: "far", Type: "notice", Subject: "s", Confidence: 0.9, Source: "observed", Prov: knowledge.Provenance{Hops: 2}},
		{ID: "old", Type: "notice", Subject: "s", Confidence: 0.9, Source: "observed", Expiry: &exp},
	} {
		if _, err := ana.kb.Add(f); err != nil {
			t.Fatal(err)
		}
	}
	sc := spread.DefaultConfig()
	sc.MaxHops = 2
	sp, err := spread.New(sc, m.Spread(), spread.WithClock(stepClock(time.Millisecond)))
	if err != nil {
		t.Fatal(err)
	}
	nostore := &person{id: "nostore"}
	if _, err := sp.Spread(ana, []spread.Holder{bo, nil, nostore, ana}, 5); err != nil {
		t.Fatal(err)
	}
	sp.Spread(nil, nil, 1)
	sp.Spread(&person{id: "t"}, []spread.Holder{bo}, 1)
	expect(t, reg, map[string]float64{
		"rumour_spread_calls_total":                                   1,
		"rumour_spread_skipped_total|reason=nil_teller":               1,
		"rumour_spread_skipped_total|reason=no_store":                 1,
		"rumour_spread_listeners_skipped_total|reason=nil_listener":   1,
		"rumour_spread_listeners_skipped_total|reason=no_store":       1, // self-skip not counted
		"rumour_spread_withheld_total|reason=below_source_confidence": 1,
		"rumour_spread_withheld_total|reason=private":                 1,
		"rumour_spread_withheld_total|reason=max_hops":                1,
		"rumour_spread_withheld_total|reason=expired":                 1,
		"rumour_spread_offered_total":                                 1,
		"rumour_spread_deliveries_total|outcome=inserted":             1,
		"rumour_spread_delivered_hops":                                1,
		"rumour_spread_delivered_confidence":                          1,
		"rumour_operation_duration_seconds|op=spread":                 1, // skipped calls are not timed
	})
	if s := histSum(t, reg, "rumour_spread_delivered_hops"); s != 1 {
		t.Errorf("hops sum = %v, want 1", s)
	}
	// A rejected copy is a delivery but is not observed in the histograms.
	if _, err := sp.Spread(ana, []spread.Holder{bo}, 5); err != nil {
		t.Fatal(err)
	}
	expect(t, reg, map[string]float64{
		"rumour_spread_deliveries_total|outcome=rejected": 1,
		"rumour_spread_delivered_hops":                    1,
	})
}

func TestInferTruncatedAndDuration(t *testing.T) {
	for _, tc := range []struct {
		passes int
		trunc  float64
	}{{1, 1}, {2, 1}, {3, 0}} {
		reg, m := newMetrics(t)
		cfg := infer.DefaultConfig()
		cfg.MaxPasses = tc.passes
		rs := []infer.Rule{
			{ID: "r1", Requires: []infer.Requirement{{Type: "a"}}, Produces: infer.Product{Type: "b"}, Confidence: 0.5},
			{ID: "r2", Requires: []infer.Requirement{{Type: "b"}}, Produces: infer.Product{Type: "c"}, Confidence: 0.5},
		}
		eng, err := infer.New(cfg, rs, m.Infer(), infer.WithClock(stepClock(time.Millisecond)))
		if err != nil {
			t.Fatal(err)
		}
		kb, _ := knowledge.NewStore(knowledge.DefaultConfig())
		kb.Add(knowledge.Fact{ID: "f", Type: "a", Subject: "s", Confidence: 0.9, Source: "observed"})
		eng.Run(kb, 1)
		eng.Run(nil, 1) // ErrNilStore is timed too
		want := tc.trunc
		if tc.passes == 1 {
			want = 0 // MaxPasses 1 is never truncated
		}
		expect(t, reg, map[string]float64{
			"rumour_infer_truncated_total":                   want,
			"rumour_operation_duration_seconds|op=infer_run": 2,
		})
	}
}

func TestRulesNewMetrics(t *testing.T) {
	reg, m := newMetrics(t)
	r, err := rules.NewRegistry(rules.DefaultConfig(), rules.Layer[rctx]{Name: "city"}, rules.Layer[rctx]{Name: "home"})
	if err != nil {
		t.Fatal(err)
	}
	r.SetClock(stepClock(time.Millisecond))
	m.ObserveRules(r)
	expect(t, reg, map[string]float64{ // seeded before any event
		"rumour_rules_fired_total|layer=city":         0,
		"rumour_rules_considered_total|layer=home":    0,
		"rumour_rules_skipped_total|layer=city":       0,
		"rumour_rules_action_errors_total|layer=home": 0,
		"rumour_rules_registered|layer=city":          0,
		"rumour_rules_registered|layer=nope":          -1,
	})
	no := func(rctx) bool { return false }
	boom := func(rctx) error { return errTest }
	for _, rl := range []struct {
		layer string
		rule  rules.Rule[rctx]
	}{
		{"city", rules.Rule[rctx]{ID: "a", Event: "ev"}},
		{"city", rules.Rule[rctx]{ID: "b", Event: "ev", When: no}},
		{"home", rules.Rule[rctx]{ID: "c", Event: "ev", Do: boom}},
		{"city", rules.Rule[rctx]{ID: "a", Event: "ev"}}, // duplicate
		{"city", rules.Rule[rctx]{Event: "ev"}},          // invalid
		{"nope", rules.Rule[rctx]{ID: "z", Event: "ev"}}, // unknown layer
	} {
		r.Register(rl.layer, "", rl.rule)
	}
	r.Unregister("city", "", "b")
	r.Unregister("city", "", "b")
	r.Unregister("nope", "", "b")
	r.Register("city", "", rules.Rule[rctx]{ID: "b", Event: "ev", When: no})
	r.Process(rctx{}, "ev")
	r.Process(rctx{}, "typo")
	expect(t, reg, map[string]float64{
		"rumour_rules_register_total|result=ok":              4,
		"rumour_rules_register_total|result=duplicate":       1,
		"rumour_rules_register_total|result=invalid":         1,
		"rumour_rules_register_total|result=unknown_layer":   1,
		"rumour_rules_unregister_total|result=removed":       1,
		"rumour_rules_unregister_total|result=not_found":     2,
		"rumour_rules_registered|layer=city":                 2,
		"rumour_rules_registered|layer=home":                 1,
		"rumour_rules_registered|layer=nope":                 -1,
		"rumour_rules_considered_total|layer=city":           2,
		"rumour_rules_skipped_total|layer=city":              1,
		"rumour_rules_considered_total|layer=home":           1,
		"rumour_rules_action_errors_total|layer=home":        1,
		"rumour_rules_fired_total|layer=city":                1,
		"rumour_rules_unmatched_total":                       1,
		"rumour_operation_duration_seconds|op=rules_process": 2,
	})
}

func TestNoClockMeansNoDurations(t *testing.T) {
	reg, m := newMetrics(t)
	cfg := knowledge.DefaultConfig()
	ana, bo := newPerson(t, "ana", cfg), newPerson(t, "bo", cfg)
	ana.kb.Add(fact("x"))
	sp, _ := spread.New(spread.DefaultConfig(), m.Spread())
	sp.Spread(ana, []spread.Holder{bo}, 1)
	eng, _ := infer.New(infer.DefaultConfig(), nil, m.Infer())
	eng.Run(ana.kb, 1)
	kb, _ := knowledge.NewStore(cfg, m.Store())
	kb.Find(knowledge.Query{}, 0)
	kb.Prune(1)
	for _, op := range []string{"spread", "infer_run", "fade", "prune", "rules_process", "store_find"} {
		if got := get(t, reg, "rumour_operation_duration_seconds", "op="+op); got != 0 {
			t.Errorf("op %s observations = %v, want 0 (series seeded)", op, got)
		}
	}
}

func TestNewSeedsClosedSets(t *testing.T) {
	reg, _ := newMetrics(t)
	exp := map[string]float64{
		"rumour_store_size": 0, "rumour_store_size_max": 0,
		"rumour_spread_calls_total": 0, "rumour_prune_runs_total": 0, "rumour_infer_truncated_total": 0,
		"rumour_rules_unmatched_total":       0,
		"rumour_fade_runs_total|fader=wired": 0, "rumour_fade_runs_total|fader=none": 0,
		"rumour_rules_register_total|result=ok": 0, "rumour_rules_unregister_total|result=removed": 0,
		"rumour_spread_delivered_hops": 0, "rumour_spread_delivered_confidence": 0,
		"rumour_fact_adds_total|outcome=inserted|source=other": 0,
		"rumour_rules_registered|layer=city":                   -1, // layers only via ObserveRules
		"rumour_rules_considered_total|layer=city":             -1,
	}
	for _, r := range spread.SkipReasons() {
		exp["rumour_spread_skipped_total|reason="+r] = 0
	}
	for _, r := range knowledge.AddErrorReasons() {
		exp["rumour_fact_add_errors_total|reason="+r] = 0
	}
	spread.SkippedListeners{}.Each(func(r string, _ int) { exp["rumour_spread_listeners_skipped_total|reason="+r] = 0 })
	spread.Withheld{}.Each(func(r string, _ int) { exp["rumour_spread_withheld_total|reason="+r] = 0 })
	for _, op := range []string{"spread", "infer_run", "fade", "prune", "rules_process", "store_find"} {
		exp["rumour_operation_duration_seconds|op="+op] = 0
	}
	expect(t, reg, exp)
}

var errTest = errString("boom")

type errString string

func (e errString) Error() string { return string(e) }
