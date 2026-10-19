package rumourotel_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/rules"
	"github.com/Whealinghold/rumour_mill/spread"

	rumourotel "github.com/Whealinghold/rumour_mill/contrib/otel"
)

type person struct {
	id string
	kb *knowledge.Store
}

func (p *person) ID() string                  { return p.id }
func (p *person) Knowledge() *knowledge.Store { return p.kb }

type stranger struct{ person }

var notice = knowledge.Fact{ID: "x", Type: "notice", Subject: "s", Confidence: 0.9, Source: "observed"}

// fakeClock steps by d on every read.
func fakeClock(d time.Duration) func() time.Time {
	t := time.Unix(0, 0)
	return func() time.Time { t = t.Add(d); return t }
}

func newStore(t testing.TB, cfg knowledge.Config, opts ...knowledge.Option) *knowledge.Store {
	if t != nil {
		t.Helper()
	}
	kb, err := knowledge.NewStore(cfg, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return kb
}

func newPerson(t testing.TB, id string, opts ...knowledge.Option) *person {
	if t != nil {
		t.Helper()
	}
	return &person{id: id, kb: newStore(t, knowledge.DefaultConfig(), opts...)}
}

func newMetrics(t testing.TB, opts ...rumourotel.Option) (*rumourotel.Metrics, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	m, err := rumourotel.New(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test"), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return m, reader
}

func key(name string, attrs attribute.Set) string {
	if attrs.Len() == 0 {
		return name
	}
	parts := []string{}
	for _, kv := range attrs.ToSlice() {
		parts = append(parts, string(kv.Key)+"="+kv.Value.AsString())
	}
	return name + "{" + strings.Join(parts, ",") + "}"
}

func collect(t testing.TB, r *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	if t != nil {
		t.Helper()
	}
	var rm metricdata.ResourceMetrics
	if err := r.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	return rm
}

// snapshot returns every counter and gauge data point as "name" or
// "name{key=val}" -> value, and every histogram as the same key -> count.
func snapshot(t testing.TB, r *sdkmetric.ManualReader) map[string]int64 {
	if t != nil {
		t.Helper()
	}
	out := map[string]int64{}
	for _, sm := range collect(t, r).ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					out[key(m.Name, dp.Attributes)] = dp.Value
				}
			case metricdata.Gauge[int64]:
				for _, dp := range d.DataPoints {
					out[key(m.Name, dp.Attributes)] = dp.Value
				}
			case metricdata.Histogram[float64]:
				for _, dp := range d.DataPoints {
					out[key(m.Name, dp.Attributes)] = int64(dp.Count)
				}
			}
		}
	}
	return out
}

// histograms returns name -> (bounds, sum) for each histogram data point key.
func histograms(t testing.TB, r *sdkmetric.ManualReader) map[string]metricdata.HistogramDataPoint[float64] {
	out := map[string]metricdata.HistogramDataPoint[float64]{}
	for _, sm := range collect(t, r).ScopeMetrics {
		for _, m := range sm.Metrics {
			if d, ok := m.Data.(metricdata.Histogram[float64]); ok {
				for _, dp := range d.DataPoints {
					out[key(m.Name, dp.Attributes)] = dp
				}
			}
		}
	}
	return out
}

// check fails for every wanted series that is missing or differs.
func check(t *testing.T, r *sdkmetric.ManualReader, want map[string]int64) {
	t.Helper()
	got := snapshot(t, r)
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			t.Errorf("%s = %d (present=%v), want %d", k, g, ok, w)
		}
	}
}

func TestDefaultsAreFreshSlices(t *testing.T) {
	for name, tc := range map[string]struct {
		f    func() []float64
		want []float64
	}{
		"duration":   {rumourotel.DefaultDurationBuckets, []float64{1e-6, 5e-6, 2.5e-5, 1e-4, 5e-4, 2.5e-3, 1e-2, 5e-2, 0.25, 1}},
		"hops":       {rumourotel.DefaultHopBuckets, []float64{1, 2, 3, 4, 5, 6, 8, 10, 15, 20}},
		"confidence": {rumourotel.DefaultConfidenceBuckets, []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1}},
	} {
		t.Run(name, func(t *testing.T) {
			got := tc.f()
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			got[0] = 99
			if tc.f()[0] == 99 {
				t.Error("default slice is shared")
			}
		})
	}
}

func TestZeroSeriesAfterNew(t *testing.T) {
	_, reader := newMetrics(t, rumourotel.WithSourceKinds("seen", "heard", "seen"))
	want := map[string]int64{}
	for _, name := range []string{
		"rumour.spread.calls", "rumour.spread.offered", "rumour.spread.errors", "rumour.holder.type_mismatch",
		"rumour.fact.evictions", "rumour.fact.pruned", "rumour.prune.runs",
		"rumour.fade.examined", "rumour.fade.changed", "rumour.fade.forgotten", "rumour.fade.invalid",
		"rumour.infer.runs", "rumour.infer.passes", "rumour.infer.errors", "rumour.infer.truncated",
		"rumour.rules.processed", "rumour.rules.errors", "rumour.rules.unmatched",
		"rumour.store.size", "rumour.store.size.max",
		"rumour.spread.withheld{reason=private}", "rumour.spread.withheld{reason=max_hops}",
		"rumour.spread.withheld{reason=below_source_confidence}", "rumour.spread.withheld{reason=expired}",
		"rumour.spread.skipped{reason=nil_teller}", "rumour.spread.skipped{reason=no_store}",
		"rumour.spread.listeners.skipped{reason=nil_listener}", "rumour.spread.listeners.skipped{reason=no_store}",
		"rumour.fact.add.errors{reason=invalid}", "rumour.fact.add.errors{reason=identity_mismatch}",
		"rumour.fade.runs{fader=wired}", "rumour.fade.runs{fader=none}",
		"rumour.rules.register{result=ok}", "rumour.rules.register{result=duplicate}",
		"rumour.rules.register{result=invalid}", "rumour.rules.register{result=unknown_layer}",
		"rumour.rules.unregister{result=removed}", "rumour.rules.unregister{result=not_found}",
		"rumour.spread.dropped{reason=filtered}", "rumour.spread.dropped{reason=back_to_source}",
		"rumour.spread.dropped{reason=below_min_confidence}", "rumour.spread.dropped{reason=transform_dropped}",
		"rumour.rules.stopped{mode=layer}", "rumour.rules.stopped{mode=all}",
		"rumour.infer.derived{outcome=inserted}", "rumour.infer.derived{outcome=replaced}",
	} {
		want[name] = 0
	}
	for _, o := range []string{"rejected", "inserted", "replaced"} {
		want["rumour.spread.deliveries{outcome="+o+"}"] = 0
		for _, s := range []string{"seen", "heard", "other"} {
			want["rumour.fact.adds{outcome="+o+",source="+s+"}"] = 0
		}
	}
	got := snapshot(t, reader)
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			t.Errorf("%s = %d (present=%v), want 0", k, g, ok)
		}
	}
	for k := range got { // layers are seeded by ObserveRules; histograms are not seeded
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected seeded series %s", k)
		}
	}
}

func TestNewErrors(t *testing.T) {
	boom := errors.New("boom")
	nan, inf := math.NaN(), math.Inf(1)
	for name, tc := range map[string]struct {
		opts  []rumourotel.Option
		meter *failingMeter
	}{
		"empty duration":    {opts: []rumourotel.Option{rumourotel.WithDurationBuckets()}},
		"nan hops":          {opts: []rumourotel.Option{rumourotel.WithHopBuckets(1, nan)}},
		"inf confidence":    {opts: []rumourotel.Option{rumourotel.WithConfidenceBuckets(0.5, inf)}},
		"descending":        {opts: []rumourotel.Option{rumourotel.WithHopBuckets(2, 1)}},
		"equal":             {opts: []rumourotel.Option{rumourotel.WithDurationBuckets(1, 1)}},
		"empty source kind": {opts: []rumourotel.Option{rumourotel.WithSourceKinds("a", "")}},
		"counter fails":     {meter: &failingMeter{err: boom, counter: true}},
		"gauge fails":       {meter: &failingMeter{err: boom, gauge: true}},
		"histogram fails":   {meter: &failingMeter{err: boom, hist: true}},
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			if tc.meter != nil {
				_, err = rumourotel.New(*tc.meter)
				if !errors.Is(err, boom) {
					t.Errorf("err = %v, want boom", err)
				}
				return
			}
			_, err = rumourotel.New(sdkmetric.NewMeterProvider().Meter("t"), tc.opts...)
			if err == nil {
				t.Error("want error")
			}
		})
	}
	// "other" and nil options are accepted.
	if _, err := rumourotel.New(sdkmetric.NewMeterProvider().Meter("t"), nil, rumourotel.WithSourceKinds("other")); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestSpread(t *testing.T) {
	m, reader := newMetrics(t)
	ana, bo, cy := newPerson(t, "ana"), newPerson(t, "bo"), newPerson(t, "cy")
	ana.kb.Add(notice)
	onlyBo := spread.Typed(func(e spread.TypedEncounter[*person]) bool { return e.Listener.id == "bo" })
	sp, _ := spread.New(spread.DefaultConfig(), m.Spread(), spread.WithFilter(onlyBo))

	if _, err := sp.Spread(ana, []spread.Holder{bo, cy}, 1); err != nil {
		t.Fatal(err)
	}
	check(t, reader, map[string]int64{
		"rumour.spread.calls": 1, "rumour.spread.offered": 2, "rumour.spread.deliveries{outcome=inserted}": 1,
		"rumour.spread.dropped{reason=filtered}": 1, "rumour.holder.type_mismatch": 0, "rumour.spread.errors": 0,
		"rumour.spread.delivered.hops": 1, "rumour.spread.delivered.confidence": 1,
	})
	if _, err := sp.Spread(ana, []spread.Holder{&stranger{*bo}}, 2); err != nil {
		t.Fatal(err)
	}
	check(t, reader, map[string]int64{
		"rumour.spread.calls": 2, "rumour.spread.offered": 3, "rumour.spread.dropped{reason=filtered}": 2,
		"rumour.holder.type_mismatch": 1,
	})
}

func TestSpreadSkipsAndListenerSkips(t *testing.T) {
	m, reader := newMetrics(t)
	ana := newPerson(t, "ana")
	ana.kb.Add(notice)
	sp, _ := spread.New(spread.DefaultConfig(), m.Spread())
	noStore := &person{id: "ns"}
	sp.Spread(nil, nil, 1)
	sp.Spread(noStore, nil, 1)
	sp.Spread(ana, []spread.Holder{nil, noStore, ana}, 1) // self is silent
	check(t, reader, map[string]int64{
		"rumour.spread.skipped{reason=nil_teller}": 1, "rumour.spread.skipped{reason=no_store}": 1,
		"rumour.spread.calls": 1,
		"rumour.spread.listeners.skipped{reason=nil_listener}": 1, "rumour.spread.listeners.skipped{reason=no_store}": 1,
	})
}

func TestSpreadErrorCounted(t *testing.T) {
	m, reader := newMetrics(t)
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	ana.kb.Add(notice)
	clash := notice
	clash.Type = "other"
	bo.kb.Add(clash)
	sp, _ := spread.New(spread.DefaultConfig(), m.Spread())
	if _, err := sp.Spread(ana, []spread.Holder{bo}, 1); !errors.Is(err, knowledge.ErrIdentityMismatch) {
		t.Fatalf("err = %v", err)
	}
	check(t, reader, map[string]int64{"rumour.spread.errors": 1, "rumour.spread.offered": 1, "rumour.spread.deliveries{outcome=inserted}": 0})
}

func TestEveryDropReasonCounted(t *testing.T) {
	nan := func(_ spread.Encounter, f knowledge.Fact) knowledge.Fact { f.Confidence = math.NaN(); return f }
	low := spread.DefaultConfig()
	low.DecayFactor, low.MinConfidence = 0.5, 0.9
	for _, tc := range []struct {
		reason string
		cfg    spread.Config
		opts   []spread.Option
		back   bool
		tf     spread.Transform
	}{
		{reason: "filtered", cfg: spread.DefaultConfig(), opts: []spread.Option{spread.WithFilter(func(spread.Encounter) bool { return false })}},
		{reason: "back_to_source", cfg: spread.DefaultConfig(), back: true},
		{reason: "below_min_confidence", cfg: low},
		{reason: "transform_dropped", cfg: spread.DefaultConfig(), tf: nan},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			m, reader := newMetrics(t)
			ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
			ana.kb.Add(notice)
			sp, _ := spread.New(tc.cfg, append(tc.opts, m.Spread())...)
			sp.Transform = tc.tf
			sp.Spread(ana, []spread.Holder{bo}, 1)
			if tc.back {
				sp.Spread(bo, []spread.Holder{ana}, 2)
			}
			check(t, reader, map[string]int64{"rumour.spread.dropped{reason=" + tc.reason + "}": 1})
		})
	}
}

func TestEveryWithheldReasonCounted(t *testing.T) {
	m, reader := newMetrics(t)
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	exp := knowledge.Tick(1)
	add := func(id string, mod func(*knowledge.Fact)) {
		f := knowledge.Fact{ID: id, Type: "t", Subject: id, Confidence: 0.9, Source: "observed"}
		mod(&f)
		if _, err := ana.kb.Add(f); err != nil {
			t.Fatal(err)
		}
	}
	add("priv", func(f *knowledge.Fact) { f.Private = true })
	add("low", func(f *knowledge.Fact) { f.Confidence = 0.01 })
	add("old", func(f *knowledge.Fact) { f.Expiry = &exp })
	add("ok", func(*knowledge.Fact) {})
	cfg := spread.DefaultConfig()
	cfg.MinSourceConfidence = 0.5
	sp, _ := spread.New(cfg, m.Spread())
	if _, err := sp.Spread(ana, []spread.Holder{bo}, 5); err != nil {
		t.Fatal(err)
	}
	check(t, reader, map[string]int64{
		"rumour.spread.withheld{reason=private}": 1, "rumour.spread.withheld{reason=below_source_confidence}": 1,
		"rumour.spread.withheld{reason=expired}": 1, "rumour.spread.withheld{reason=max_hops}": 0,
		"rumour.spread.offered": 1,
	})
}

func TestDeliveredHistogramsSkipRejected(t *testing.T) {
	m, reader := newMetrics(t, rumourotel.WithHopBuckets(1, 2), rumourotel.WithConfidenceBuckets(0.5, 1))
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	ana.kb.Add(notice)
	sp, _ := spread.New(spread.DefaultConfig(), m.Spread())
	sp.Spread(ana, []spread.Holder{bo}, 1)
	sp.Spread(ana, []spread.Holder{bo}, 2) // second copy is rejected (not newer)
	h := histograms(t, reader)
	hops, conf := h["rumour.spread.delivered.hops"], h["rumour.spread.delivered.confidence"]
	if hops.Count != 1 || conf.Count != 1 {
		t.Errorf("counts hops=%d conf=%d, want 1 each (rejected copies are not observed)", hops.Count, conf.Count)
	}
	if !slices.Equal(hops.Bounds, []float64{1, 2}) || !slices.Equal(conf.Bounds, []float64{0.5, 1}) {
		t.Errorf("bounds hops=%v conf=%v", hops.Bounds, conf.Bounds)
	}
}

func TestDefaultBucketsOnInstruments(t *testing.T) {
	m, reader := newMetrics(t)
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	ana.kb.Add(notice)
	sp, _ := spread.New(spread.DefaultConfig(), m.Spread(), spread.WithClock(fakeClock(time.Millisecond)))
	sp.Spread(ana, []spread.Holder{bo}, 1)
	h := histograms(t, reader)
	for k, want := range map[string][]float64{
		"rumour.spread.delivered.hops":         rumourotel.DefaultHopBuckets(),
		"rumour.spread.delivered.confidence":   rumourotel.DefaultConfidenceBuckets(),
		"rumour.operation.duration{op=spread}": rumourotel.DefaultDurationBuckets(),
	} {
		if !slices.Equal(h[k].Bounds, want) {
			t.Errorf("%s bounds = %v, want %v", k, h[k].Bounds, want)
		}
	}
}

func TestStoreAddsEvictionsFadeAndSize(t *testing.T) {
	m, reader := newMetrics(t, rumourotel.WithSourceKinds("observed"))
	cfg := knowledge.DefaultConfig()
	cfg.MaxFacts = 2
	fader := func(f knowledge.Fact, _, _ knowledge.Tick) (float64, bool) {
		switch f.ID {
		case "gone":
			return 0, true
		case "bad":
			return math.NaN(), false
		}
		return f.Confidence / 2, false
	}
	kb := newStore(t, cfg, m.Store(), knowledge.WithFader(fader))
	add := func(id, src string) {
		kb.Add(knowledge.Fact{ID: id, Type: "t", Subject: id, Confidence: 0.5, Source: src})
	}
	add("a", "observed")
	add("b", "mystery")
	add("c", "unlisted") // evicts one; size stays 2
	check(t, reader, map[string]int64{
		"rumour.fact.adds{outcome=inserted,source=observed}": 1,
		"rumour.fact.adds{outcome=inserted,source=other}":    2,
		"rumour.fact.evictions":                              1,
		"rumour.store.size":                                  2, "rumour.store.size.max": 2,
	})
	if _, err := kb.Add(knowledge.Fact{Type: "", Source: "x"}); err == nil {
		t.Fatal("want error")
	}
	check(t, reader, map[string]int64{"rumour.fact.add.errors{reason=invalid}": 1, "rumour.fact.adds{outcome=inserted,source=other}": 2})

	kb.Prune(0)
	check(t, reader, map[string]int64{"rumour.prune.runs": 1, "rumour.fact.pruned": 0})

	cfg.MaxFacts = 0
	fk := newStore(t, cfg, m.Store(), knowledge.WithFader(fader))
	for _, id := range []string{"gone", "bad", "half"} {
		fk.Add(knowledge.Fact{ID: id, Type: "t", Subject: id, Confidence: 0.8, Source: "observed"})
	}
	fk.Fade(10)
	check(t, reader, map[string]int64{
		"rumour.fade.runs{fader=wired}": 1, "rumour.fade.examined": 3, "rumour.fade.changed": 1,
		"rumour.fade.forgotten": 1, "rumour.fade.invalid": 1, "rumour.store.size": 2, "rumour.store.size.max": 3,
	})
	newStore(t, knowledge.DefaultConfig(), m.Store()).Fade(10) // no Fader: still counted
	check(t, reader, map[string]int64{"rumour.fade.runs{fader=none}": 1, "rumour.fade.runs{fader=wired}": 1, "rumour.fade.examined": 3, "rumour.store.size": 0, "rumour.store.size.max": 3})
}

func TestIdentityMismatchAddError(t *testing.T) {
	m, reader := newMetrics(t)
	kb := newStore(t, knowledge.DefaultConfig(), m.Store())
	kb.Add(notice)
	clash := notice
	clash.Type = "other"
	if _, err := kb.Add(clash); !errors.Is(err, knowledge.ErrIdentityMismatch) {
		t.Fatalf("err = %v", err)
	}
	check(t, reader, map[string]int64{"rumour.fact.add.errors{reason=identity_mismatch}": 1})
}

func TestPruneCounted(t *testing.T) {
	m, reader := newMetrics(t)
	kb := newStore(t, knowledge.DefaultConfig(), m.Store())
	exp := knowledge.Tick(5)
	for _, id := range []string{"a", "b"} {
		kb.Add(knowledge.Fact{ID: id, Type: "t", Subject: id, Confidence: 0.5, Source: "observed", Expiry: &exp})
	}
	kb.Prune(100)
	check(t, reader, map[string]int64{"rumour.fact.pruned": 2, "rumour.prune.runs": 1, "rumour.store.size": 0, "rumour.store.size.max": 2})
	kb.Prune(200)
	check(t, reader, map[string]int64{"rumour.fact.pruned": 2, "rumour.prune.runs": 2})
}

func TestDurations(t *testing.T) {
	m, reader := newMetrics(t)
	clk := fakeClock(2 * time.Millisecond)
	ana, bo := newPerson(t, "ana", m.Store()), newPerson(t, "bo")
	_ = bo
	kb := newStore(t, knowledge.DefaultConfig(), m.Store(), knowledge.WithClock(clk))
	kb.Add(notice)
	kb.Find(knowledge.Query{}, 1)
	kb.Fade(1)
	kb.Prune(1)
	sp, _ := spread.New(spread.DefaultConfig(), m.Spread(), spread.WithClock(clk))
	sp.Spread(ana, []spread.Holder{bo}, 1)
	en, _ := infer.New(infer.DefaultConfig(), nil, m.Infer(), infer.WithClock(clk))
	en.Run(kb, 1)
	reg, _ := rules.NewRegistry(rules.DefaultConfig(), rules.Layer[int]{Name: "world"})
	reg.SetClock(clk)
	m.ObserveRules(reg)
	reg.Process(0, "ping")

	got := snapshot(t, reader)
	for _, op := range []string{"spread", "infer_run", "fade", "prune", "store_find", "rules_process"} {
		if got["rumour.operation.duration{op="+op+"}"] < 1 {
			t.Errorf("no duration observed for op %s", op)
		}
	}
	dp := histograms(t, reader)["rumour.operation.duration{op=rules_process}"]
	if dp.Sum != 0.002 {
		t.Errorf("rules_process sum = %v, want 0.002 (one clock step)", dp.Sum)
	}
}

func TestNoClockNoDurations(t *testing.T) {
	m, reader := newMetrics(t)
	kb := newStore(t, knowledge.DefaultConfig(), m.Store())
	kb.Add(notice)
	kb.Find(knowledge.Query{}, 1)
	kb.Fade(1)
	for k := range snapshot(t, reader) {
		if strings.HasPrefix(k, "rumour.operation.duration") {
			t.Errorf("unexpected %s without a clock", k)
		}
	}
}

func TestAddsSeeSpreadAndInfer(t *testing.T) {
	m, reader := newMetrics(t, rumourotel.WithSourceKinds("observed"))
	ana, bo := newPerson(t, "ana", m.Store()), newPerson(t, "bo", m.Store())
	ana.kb.Add(notice)
	sp, _ := spread.New(spread.DefaultConfig())
	sp.Spread(ana, []spread.Holder{bo}, 1)
	en, _ := infer.New(infer.DefaultConfig(), []infer.Rule{{
		ID: "r", Requires: []infer.Requirement{{Type: "notice"}}, Produces: infer.Product{Type: "rumour"}, Confidence: 0.5,
	}}, m.Infer())
	en.Run(bo.kb, 2)
	got := snapshot(t, reader)
	var total int64
	for k, v := range got {
		if strings.HasPrefix(k, "rumour.fact.adds{outcome=inserted") {
			total += v
		}
	}
	if total != 3 {
		t.Errorf("inserted adds = %d, want 3", total)
	}
}

func TestInfer(t *testing.T) {
	m, reader := newMetrics(t)
	kb := newStore(t, knowledge.DefaultConfig())
	kb.Add(notice)
	rule := infer.Rule{ID: "r", Requires: []infer.Requirement{{Type: "notice"}}, Produces: infer.Product{Type: "rumour"}, Confidence: 0.5}
	var other int
	en, err := infer.New(infer.DefaultConfig(), []infer.Rule{rule}, m.Infer(),
		infer.WithHooks(infer.Hooks{OnRun: func(infer.Result, error) { other++ }}), nil)
	if err != nil {
		t.Fatal(err)
	}
	en.Run(kb, 1)
	check(t, reader, map[string]int64{
		"rumour.infer.runs": 1, "rumour.infer.passes": 1, "rumour.infer.derived{outcome=inserted}": 1, "rumour.infer.errors": 0,
	})
	if _, err := en.Run(nil, 2); !errors.Is(err, infer.ErrNilStore) {
		t.Fatalf("err = %v", err)
	}
	check(t, reader, map[string]int64{"rumour.infer.runs": 2, "rumour.infer.errors": 1})
	if other != 2 {
		t.Errorf("chained hook ran %d times, want 2", other)
	}
}

func TestInferTruncated(t *testing.T) {
	chain := infer.Rule{ID: "c", Requires: []infer.Requirement{{Type: "notice"}}, Produces: infer.Product{Type: "rumour"}, Confidence: 0.5}
	chain2 := infer.Rule{ID: "d", Requires: []infer.Requirement{{Type: "rumour"}}, Produces: infer.Product{Type: "story"}, Confidence: 0.4}
	for _, tc := range []struct {
		name      string
		maxPasses int
		want      int64
	}{{"truncated", 2, 1}, {"enough", 5, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			m, reader := newMetrics(t)
			cfg := infer.DefaultConfig()
			cfg.MaxPasses = tc.maxPasses
			kb := newStore(t, knowledge.DefaultConfig())
			kb.Add(notice)
			en, err := infer.New(cfg, []infer.Rule{chain, chain2}, m.Infer())
			if err != nil {
				t.Fatal(err)
			}
			en.Run(kb, 1)
			check(t, reader, map[string]int64{"rumour.infer.truncated": tc.want})
		})
	}
}

func TestRules(t *testing.T) {
	m, reader := newMetrics(t)
	reg, err := rules.NewRegistry(rules.DefaultConfig(), rules.Layer[int]{Name: "world"}, rules.Layer[int]{Name: "local"})
	if err != nil {
		t.Fatal(err)
	}
	var other int
	m.ObserveRules(reg)
	reg.Observe(rules.Hooks{OnProcess: func(rules.Result, error) { other++ }})
	reg.Observe(rules.Hooks{}) // nil fields are ignored
	check(t, reader, map[string]int64{
		"rumour.rules.fired{layer=world}": 0, "rumour.rules.fired{layer=local}": 0,
		"rumour.rules.considered{layer=world}": 0, "rumour.rules.skipped{layer=local}": 0,
		"rumour.rules.action.errors{layer=world}": 0, "rumour.rules.registered{layer=world}": 0,
		"rumour.rules.registered{layer=local}": 0,
	})
	boom := errors.New("boom")
	must := func(layer string, r rules.Rule[int]) {
		t.Helper()
		if err := reg.Register(layer, "", r); err != nil {
			t.Fatal(err)
		}
	}
	never := func(int) bool { return false }
	must("world", rules.Rule[int]{ID: "w1", Event: "ping", Priority: 1, Stop: rules.StopLayer})
	must("world", rules.Rule[int]{ID: "w2", Event: "ping", Priority: 2})
	must("world", rules.Rule[int]{ID: "w3", Event: "other", When: never})
	must("local", rules.Rule[int]{ID: "l1", Event: "ping", Do: func(int) error { return boom }, Stop: rules.StopAll})
	must("local", rules.Rule[int]{ID: "l2", Event: "ping", Priority: 2})
	check(t, reader, map[string]int64{"rumour.rules.registered{layer=world}": 3, "rumour.rules.registered{layer=local}": 2, "rumour.rules.register{result=ok}": 5})

	if _, err := reg.Process(0, "ping"); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	check(t, reader, map[string]int64{
		"rumour.rules.processed": 1, "rumour.rules.fired{layer=world}": 1, "rumour.rules.fired{layer=local}": 1,
		"rumour.rules.stopped{mode=all}": 1, "rumour.rules.errors": 1, "rumour.rules.unmatched": 0,
		"rumour.rules.considered{layer=world}": 2, "rumour.rules.considered{layer=local}": 2,
		"rumour.rules.action.errors{layer=local}": 1, "rumour.rules.action.errors{layer=world}": 0,
	})
	reg.Process(0, "other") // candidate whose When is false
	check(t, reader, map[string]int64{"rumour.rules.skipped{layer=world}": 1, "rumour.rules.unmatched": 0})
	reg.Process(0, "typo")
	check(t, reader, map[string]int64{"rumour.rules.unmatched": 1, "rumour.rules.processed": 3})

	reg.Register("world", "", rules.Rule[int]{ID: "w1", Event: "ping"})  // duplicate
	reg.Register("world", "", rules.Rule[int]{})                         // invalid
	reg.Register("nowhere", "", rules.Rule[int]{ID: "z", Event: "ping"}) // unknown layer
	check(t, reader, map[string]int64{
		"rumour.rules.register{result=duplicate}": 1, "rumour.rules.register{result=invalid}": 1,
		"rumour.rules.register{result=unknown_layer}": 1, "rumour.rules.registered{layer=world}": 3,
	})
	if _, ok := snapshot(t, reader)["rumour.rules.registered{layer=nowhere}"]; ok {
		t.Error("unknown layer produced a gauge series")
	}
	reg.Unregister("world", "", "w2")
	reg.Unregister("world", "", "w2")
	check(t, reader, map[string]int64{
		"rumour.rules.unregister{result=removed}": 1, "rumour.rules.unregister{result=not_found}": 1,
		"rumour.rules.registered{layer=world}": 2,
	})
	if other != 3 {
		t.Errorf("chained hook ran %d times, want 3", other)
	}
}

func TestRulesWithoutObserveRulesIsLazy(t *testing.T) {
	m, reader := newMetrics(t)
	reg, _ := rules.NewRegistry(rules.DefaultConfig(), rules.Layer[int]{Name: "world"})
	reg.Observe(m.Rules())
	if _, ok := snapshot(t, reader)["rumour.rules.fired{layer=world}"]; ok {
		t.Error("layer series seeded without ObserveRules")
	}
	reg.Register("world", "", rules.Rule[int]{ID: "r", Event: "ping"})
	reg.Process(0, "ping")
	check(t, reader, map[string]int64{"rumour.rules.fired{layer=world}": 1})
}

func TestSpreadOptionChainsWithOtherHooks(t *testing.T) {
	m, reader := newMetrics(t)
	var calls, skips int
	sp, _ := spread.New(spread.DefaultConfig(), m.Spread(), spread.WithHooks(spread.Hooks{
		OnSpread: func(spread.Result, error) { calls++ }, OnSkip: func(string) { skips++ }}))
	ana := newPerson(t, "ana")
	ana.kb.Add(notice)
	sp.Spread(ana, []spread.Holder{newPerson(t, "bo")}, 1)
	sp.Spread(nil, nil, 1)
	check(t, reader, map[string]int64{"rumour.spread.offered": 1, "rumour.spread.skipped{reason=nil_teller}": 1})
	if calls != 1 || skips != 1 {
		t.Errorf("other hooks ran calls=%d skips=%d, want 1 each", calls, skips)
	}
}

// Labels must stay a bounded set: no series may carry fact, rule or event names.
func TestOnlyBoundedAttributeKeys(t *testing.T) {
	m, reader := newMetrics(t)
	ana := newPerson(t, "ana", m.Store())
	ana.kb.Add(notice)
	allowed := map[attribute.Key]bool{"outcome": true, "reason": true, "mode": true, "layer": true,
		"source": true, "fader": true, "result": true, "op": true}
	for k := range snapshot(t, reader) {
		if i := strings.IndexByte(k, '{'); i >= 0 {
			for _, kv := range strings.Split(strings.Trim(k[i:], "{}"), ",") {
				if !allowed[attribute.Key(strings.SplitN(kv, "=", 2)[0])] {
					t.Errorf("unexpected attribute in %s", k)
				}
			}
		}
	}
}

func TestDerivedNeverSeedsRejected(t *testing.T) {
	_, reader := newMetrics(t)
	if _, ok := snapshot(t, reader)["rumour.infer.derived{outcome=rejected}"]; ok {
		t.Error("rejected derived series seeded")
	}
}

func TestSizeMaxNeverDecreases(t *testing.T) {
	m, reader := newMetrics(t)
	kb := newStore(t, knowledge.DefaultConfig(), m.Store())
	exp := knowledge.Tick(5)
	for _, id := range []string{"a", "b", "c"} {
		kb.Add(knowledge.Fact{ID: id, Type: "t", Subject: id, Confidence: 0.5, Source: "x", Expiry: &exp})
	}
	kb.Prune(100)
	check(t, reader, map[string]int64{"rumour.store.size": 0, "rumour.store.size.max": 3})
}

func TestDescriptionsAndUnitsArePinned(t *testing.T) {
	_, reader := newMetrics(t)
	want := map[string]string{
		"rumour.fact.adds":       "Store adds that returned no error, by outcome and source kind (a rejected add is still an add).",
		"rumour.spread.withheld": "Facts the teller held that were never offered, by reason (private, max_hops, below_source_confidence, expired).",
		"rumour.store.size":      "Fact count of the store last touched.",
	}
	units := map[string]string{"rumour.store.size": "{fact}", "rumour.store.size.max": "{fact}", "rumour.rules.registered": "{rule}"}
	for _, sm := range collect(t, reader).ScopeMetrics {
		for _, m := range sm.Metrics {
			if w, ok := want[m.Name]; ok {
				if m.Description != w {
					t.Errorf("%s description = %q", m.Name, m.Description)
				}
				delete(want, m.Name)
			}
			if u, ok := units[m.Name]; ok {
				if m.Unit != u {
					t.Errorf("%s unit = %q, want %q", m.Name, m.Unit, u)
				}
				delete(units, m.Name)
			}
		}
	}
	if len(want) != 0 {
		t.Errorf("instruments not exported: %v", want)
	}
}
