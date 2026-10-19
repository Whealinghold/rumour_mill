package spread_test

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/spread"
)

func TestHolderMismatches(t *testing.T) {
	typedFilter := spread.WithFilter(spread.Typed(func(spread.TypedEncounter[*person]) bool { return true }))
	tests := []struct {
		name         string
		opts         []spread.Option
		transform    bool
		wantMismatch int
		wantFiltered int
		wantTransfer int
	}{
		{"typed filter mismatch counted and dropped", []spread.Option{typedFilter}, false, 2, 2, 0},
		{"typed transform mismatch counted and passed through", nil, true, 2, 0, 2},
		{"no typed policy, no mismatches", nil, false, 0, 0, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := knowledge.DefaultConfig()
			ana := newPerson("ana", cfg)
			give(t, ana, "x", 0.9)
			give(t, ana, "y", 0.9)
			stranger := otherHolder{kb: newPerson("s", cfg).kb}
			sp := mustSpreader(t, spread.DefaultConfig(), tt.opts...)
			if tt.transform {
				sp.Transform = spread.TypedTransform(func(_ spread.TypedEncounter[*person], d knowledge.Fact) knowledge.Fact {
					d.Confidence = 0.01 // must not run: holders mismatch
					return d
				})
			}
			res, err := sp.Spread(ana, []spread.Holder{stranger}, 1)
			if err != nil {
				t.Fatal(err)
			}
			if res.HolderMismatches != tt.wantMismatch || res.Dropped.Filtered != tt.wantFiltered || len(res.Transfers) != tt.wantTransfer {
				t.Fatalf("mismatches=%d filtered=%d transfers=%d, want %d/%d/%d", res.HolderMismatches, res.Dropped.Filtered, len(res.Transfers), tt.wantMismatch, tt.wantFiltered, tt.wantTransfer)
			}
			// Counter resets per call: a second Spread reports only its own.
			res2, _ := sp.Spread(ana, []spread.Holder{stranger}, 2)
			if res2.HolderMismatches != tt.wantMismatch {
				t.Fatalf("second call mismatches %d, want %d", res2.HolderMismatches, tt.wantMismatch)
			}
		})
	}
}

func TestHandBuiltEncounterNilCounter(t *testing.T) {
	f := spread.Typed(func(spread.TypedEncounter[*person]) bool { return true })
	if f(spread.Encounter{Teller: nilHolder{}, Listener: nilHolder{}}) {
		t.Fatal("mismatch must still fail closed")
	}
	tr := spread.TypedTransform(func(_ spread.TypedEncounter[*person], d knowledge.Fact) knowledge.Fact { return d })
	if got := tr(spread.Encounter{Teller: nilHolder{}}, knowledge.Fact{ID: "k"}); got.ID != "k" {
		t.Fatalf("passthrough lost: %+v", got)
	}
}

func TestMismatchWarnOncePerCall(t *testing.T) {
	cfg := knowledge.DefaultConfig()
	ana := newPerson("ana", cfg)
	give(t, ana, "x", 0.9)
	give(t, ana, "y", 0.9)
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	sp := mustSpreader(t, spread.DefaultConfig(), spread.WithLogger(log),
		spread.WithFilter(spread.Typed(func(spread.TypedEncounter[*person]) bool { return true })))
	l := []spread.Holder{otherHolder{kb: newPerson("s", cfg).kb}, otherHolder{kb: newPerson("t", cfg).kb}}
	sp.Spread(ana, l, 1) // 4 mismatching pairs
	if n := strings.Count(buf.String(), "level=WARN"); n != 1 {
		t.Fatalf("%d warnings, want 1: %q", n, buf.String())
	}
	if !strings.Contains(buf.String(), "mismatches=4") {
		t.Errorf("warning lacks count: %q", buf.String())
	}
	buf.Reset()
	sp.Spread(ana, holders(newPerson("bo", cfg)), 2) // all match
	if buf.Len() != 0 {
		t.Errorf("unexpected warning: %q", buf.String())
	}
}

func TestOnSpreadReceivesJoinedError(t *testing.T) {
	cfg := knowledge.DefaultConfig()
	ana, bo := newPerson("ana", cfg), newPerson("bo", cfg)
	give(t, ana, "x", 0.9)
	// Same ID, different subject: bo's store refuses the copy.
	if _, err := bo.kb.Add(knowledge.Fact{ID: "x", Type: "t", Subject: "other", Confidence: 0.9, Source: "observed"}); err != nil {
		t.Fatal(err)
	}
	var gotErr error
	calls := 0
	sp := mustSpreader(t, spread.DefaultConfig(), spread.WithHooks(spread.Hooks{OnSpread: func(_ spread.Result, err error) { calls++; gotErr = err }}))
	_, err := sp.Spread(ana, holders(bo), 1)
	if err == nil || calls != 1 || !errors.Is(gotErr, knowledge.ErrIdentityMismatch) || gotErr.Error() != err.Error() {
		t.Fatalf("calls=%d hook err=%v returned err=%v", calls, gotErr, err)
	}
	// Success path: nil error.
	ok := mustSpreader(t, spread.DefaultConfig(), spread.WithHooks(spread.Hooks{OnSpread: func(_ spread.Result, err error) { gotErr = err }}))
	gotErr = errors.New("sentinel")
	ok.Spread(ana, holders(newPerson("cy", cfg)), 1)
	if gotErr != nil {
		t.Fatalf("success path err %v", gotErr)
	}
}

func warnLog(buf *strings.Builder) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestWithheldCountsFactsNotPairs(t *testing.T) {
	cfg := knowledge.DefaultConfig()
	ana := newPerson("ana", cfg)
	give(t, ana, "open", 0.9)
	ana.kb.Add(knowledge.Fact{ID: "priv", Type: "t", Subject: "s", Confidence: 0.9, Source: "observed", Private: true})
	ana.kb.Add(knowledge.Fact{ID: "far", Type: "t", Subject: "s", Confidence: 0.9, Source: "observed", Prov: knowledge.Provenance{Hops: 2}})
	sc := spread.DefaultConfig()
	sc.MaxHops = 2
	sp := mustSpreader(t, sc)
	res, err := sp.Spread(ana, holders(newPerson("bo", cfg), newPerson("cy", cfg)), 1) // two listeners
	if err != nil {
		t.Fatal(err)
	}
	if res.Withheld != (spread.Withheld{Private: 1, MaxHops: 1}) {
		t.Fatalf("withheld %+v, want one private and one max_hops (facts, not pairs)", res.Withheld)
	}
	if res.Offered != 2 || len(res.Transfers) != 2 || res.Dropped != (spread.Dropped{}) {
		t.Fatalf("withheld leaked into offered/dropped: %+v", res)
	}
	var got []string
	res.Withheld.Each(func(r string, n int) { got = append(got, fmt.Sprint(r, "=", n)) })
	if fmt.Sprint(got) != "[private=1 max_hops=1 below_source_confidence=0 expired=0]" {
		t.Fatalf("Each = %v", got)
	}
	got = nil
	spread.Withheld{}.Each(func(r string, n int) { got = append(got, fmt.Sprint(r, "=", n)) })
	if fmt.Sprint(got) != "[private=0 max_hops=0 below_source_confidence=0 expired=0]" {
		t.Fatalf("zero Each = %v", got)
	}
}

func TestSpreadWarnings(t *testing.T) {
	cfg := knowledge.DefaultConfig()
	t.Run("store errors once per call", func(t *testing.T) {
		ana, bo, cy := newPerson("ana", cfg), newPerson("bo", cfg), newPerson("cy", cfg)
		give(t, ana, "x", 0.9)
		give(t, ana, "y", 0.9)
		for _, p := range []*person{bo, cy} {
			for _, id := range []string{"x", "y"} {
				p.kb.Add(knowledge.Fact{ID: id, Type: "t", Subject: "other", Confidence: 0.9, Source: "observed"})
			}
		}
		var buf strings.Builder
		sp := mustSpreader(t, spread.DefaultConfig(), spread.WithLogger(warnLog(&buf)))
		sp.Spread(ana, holders(bo, cy), 1)
		if n := strings.Count(buf.String(), "level=WARN"); n != 1 || !strings.Contains(buf.String(), "errors=4") || !strings.Contains(buf.String(), "teller=ana") {
			t.Fatalf("want one warn with teller and errors=4: %q", buf.String())
		}
	})
	t.Run("transform dropped once per call", func(t *testing.T) {
		ana := newPerson("ana", cfg)
		give(t, ana, "x", 0.9)
		give(t, ana, "y", 0.9)
		var buf strings.Builder
		sp := mustSpreader(t, spread.DefaultConfig(), spread.WithLogger(warnLog(&buf)))
		sp.Transform = func(_ spread.Encounter, d knowledge.Fact) knowledge.Fact { d.Confidence = math.NaN(); return d }
		res, _ := sp.Spread(ana, holders(newPerson("bo", cfg)), 1)
		if res.Dropped.TransformDropped != 2 || strings.Count(buf.String(), "level=WARN") != 1 || !strings.Contains(buf.String(), "dropped=2") {
			t.Fatalf("res %+v log %q", res.Dropped, buf.String())
		}
	})
	t.Run("nil teller warns, no hook", func(t *testing.T) {
		var buf strings.Builder
		calls := 0
		sp := mustSpreader(t, spread.DefaultConfig(), spread.WithLogger(warnLog(&buf)),
			spread.WithHooks(spread.Hooks{OnSpread: func(spread.Result, error) { calls++ }}))
		sp.Spread(nil, nil, 1)
		sp.Spread(nilHolder{}, nil, 1)
		if n := strings.Count(buf.String(), "level=WARN"); n != 2 || calls != 0 {
			t.Fatalf("warns=%d hook calls=%d: %q", n, calls, buf.String())
		}
	})
}

func TestDebugSummaryAndLoggerOptions(t *testing.T) {
	cfg := knowledge.DefaultConfig()
	ana := newPerson("ana", cfg)
	give(t, ana, "x", 0.9)
	ana.kb.Add(knowledge.Fact{ID: "p", Type: "t", Subject: "s", Confidence: 0.9, Source: "observed", Private: true})
	var buf strings.Builder
	sp := mustSpreader(t, spread.DefaultConfig(), spread.WithLogger(warnLog(&buf)))
	sp.Spread(ana, holders(newPerson("bo", cfg)), 1)
	for _, want := range []string{"level=DEBUG", "msg=spread", "teller=ana", "offered=1", "delivered=1", "dropped=", "errors=0", "withheld="} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("summary lacks %q: %q", want, buf.String())
		}
	}
	if !strings.Contains(buf.String(), "Private:1") && !strings.Contains(buf.String(), "{1 0}") {
		t.Errorf("withheld value missing: %q", buf.String())
	}

	// WithLogger(nil) keeps the previous logger.
	buf.Reset()
	sp2 := mustSpreader(t, spread.DefaultConfig(), spread.WithLogger(warnLog(&buf)), spread.WithLogger(nil))
	sp2.Spread(ana, holders(newPerson("cy", cfg)), 1)
	if buf.Len() == 0 {
		t.Error("WithLogger(nil) replaced the logger")
	}

	// Default logger is silent: it must not write to the default slog output.
	var def strings.Builder
	old := slog.Default()
	slog.SetDefault(warnLog(&def))
	defer slog.SetDefault(old)
	sp3 := mustSpreader(t, spread.DefaultConfig())
	sp3.Spread(ana, holders(newPerson("di", cfg)), 1)
	sp3.Spread(nil, nil, 1)
	if def.Len() != 0 {
		t.Errorf("default logger not silent: %q", def.String())
	}
}

func TestTypedTransformForeignTeller(t *testing.T) {
	cfg := knowledge.DefaultConfig()
	stranger := otherHolder{kb: newPerson("s", cfg).kb}
	stranger.kb.Add(knowledge.Fact{ID: "f", Type: "t", Subject: "s", Confidence: 0.9, Source: "observed"})
	bo := newPerson("bo", cfg)
	called := false
	sp := mustSpreader(t, spread.DefaultConfig())
	sp.Transform = spread.TypedTransform(func(_ spread.TypedEncounter[*person], d knowledge.Fact) knowledge.Fact {
		called = true
		d.Confidence = 0.01
		return d
	})
	res, err := sp.Spread(stranger, holders(bo), 1)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := bo.kb.Get("f")
	if called || res.HolderMismatches != 1 || !ok || math.Abs(f.Confidence-0.72) > 1e-9 {
		t.Fatalf("called=%v mismatches=%d ok=%v conf=%v", called, res.HolderMismatches, ok, f.Confidence)
	}
}

type noStoreHolder struct{ id string }

func (h noStoreHolder) ID() string                { return h.id }
func (noStoreHolder) Knowledge() *knowledge.Store { return nil }

// stepClock returns a fake clock advancing step per call.
func stepClock(step time.Duration) func() time.Time {
	t := time.Unix(0, 0)
	return func() time.Time { t = t.Add(step); return t }
}

func TestWithheldAllReasonsAndOffered(t *testing.T) {
	cfg := knowledge.DefaultConfig()
	ana := newPerson("ana", cfg)
	exp := knowledge.Tick(5)
	add := func(f knowledge.Fact) {
		f.Type, f.Subject, f.Source = "t", "s", "observed"
		if _, err := ana.kb.Add(f); err != nil {
			t.Fatal(err)
		}
	}
	add(knowledge.Fact{ID: "ok", Confidence: 0.9})
	add(knowledge.Fact{ID: "old", Confidence: 0.9, Expiry: &exp})
	add(knowledge.Fact{ID: "weak", Confidence: 0.1})
	add(knowledge.Fact{ID: "priv", Confidence: 0.9, Private: true})
	add(knowledge.Fact{ID: "far", Confidence: 0.9, Prov: knowledge.Provenance{Hops: 2}})
	sc := spread.DefaultConfig()
	sc.MaxHops = 2
	res, err := mustSpreader(t, sc).Spread(ana, holders(newPerson("bo", cfg)), 10)
	if err != nil {
		t.Fatal(err)
	}
	want := spread.Withheld{Expired: 1, BelowSourceConfidence: 1, Private: 1, MaxHops: 1}
	if res.Withheld != want {
		t.Fatalf("withheld %+v, want %+v", res.Withheld, want)
	}
	if res.Offered != 1 || len(res.Transfers) != 1 || res.Transfers[0].Hops != 1 {
		t.Fatalf("offered=%d transfers=%+v", res.Offered, res.Transfers)
	}
}

func TestSkippedListeners(t *testing.T) {
	cfg := knowledge.DefaultConfig()
	ana := newPerson("ana", cfg)
	give(t, ana, "x", 0.9)
	tests := []struct {
		name  string
		l     []spread.Holder
		want  spread.SkippedListeners
		warns bool
	}{
		{"none", holders(newPerson("bo", cfg)), spread.SkippedListeners{}, false},
		{"nil", []spread.Holder{nil, nil}, spread.SkippedListeners{Nil: 2}, true},
		{"no store", []spread.Holder{noStoreHolder{"z"}}, spread.SkippedListeners{NoStore: 1}, true},
		{"self is silent", []spread.Holder{ana}, spread.SkippedListeners{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf strings.Builder
			sp := mustSpreader(t, spread.DefaultConfig(), spread.WithLogger(warnLog(&buf)))
			res, err := sp.Spread(ana, tt.l, 1)
			if err != nil || res.SkippedListeners != tt.want {
				t.Fatalf("skipped %+v err %v, want %+v", res.SkippedListeners, err, tt.want)
			}
			if got := strings.Contains(buf.String(), "listeners skipped"); got != tt.warns {
				t.Fatalf("warn=%v want %v: %s", got, tt.warns, buf.String())
			}
		})
	}
	var got []string
	spread.SkippedListeners{Nil: 1}.Each(func(r string, n int) { got = append(got, fmt.Sprint(r, "=", n)) })
	if fmt.Sprint(got) != "[nil_listener=1 no_store=0]" {
		t.Fatalf("Each = %v", got)
	}
}

func TestOnSkipAndClock(t *testing.T) {
	cfg := knowledge.DefaultConfig()
	if got := spread.SkipReasons(); fmt.Sprint(got) != "[nil_teller no_store]" {
		t.Fatalf("SkipReasons %v", got)
	}
	var skips []string
	spreads, dones := 0, 0
	var d time.Duration
	h := spread.Hooks{
		OnSkip:       func(r string) { skips = append(skips, r) },
		OnSpread:     func(spread.Result, error) { spreads++ },
		OnSpreadDone: func(x time.Duration) { dones++; d = x },
	}
	sp := mustSpreader(t, spread.DefaultConfig(), spread.WithHooks(h), spread.WithClock(stepClock(time.Millisecond)))
	sp.Spread(nil, nil, 1)
	sp.Spread(noStoreHolder{"z"}, nil, 1)
	if fmt.Sprint(skips) != "[nil_teller no_store]" || spreads != 0 || dones != 0 {
		t.Fatalf("skips=%v spreads=%d dones=%d", skips, spreads, dones)
	}
	sp.Spread(newPerson("ana", cfg), nil, 1)
	if spreads != 1 || dones != 1 || d != time.Millisecond {
		t.Fatalf("spreads=%d dones=%d d=%v", spreads, dones, d)
	}
	// No clock: OnSpreadDone never fires. Nil clock option is ignored.
	dones = 0
	sp = mustSpreader(t, spread.DefaultConfig(), spread.WithHooks(h), spread.WithClock(nil))
	sp.Spread(newPerson("ana", cfg), nil, 1)
	if dones != 0 {
		t.Fatal("OnSpreadDone fired without a clock")
	}
	// A backwards clock floors at zero.
	back := time.Unix(100, 0)
	sp = mustSpreader(t, spread.DefaultConfig(), spread.WithHooks(h), spread.WithClock(func() time.Time { back = back.Add(-time.Second); return back }))
	sp.Spread(newPerson("ana", cfg), nil, 1)
	if d != 0 {
		t.Fatalf("d=%v, want floor 0", d)
	}
}

func TestWithHooksChainsEveryField(t *testing.T) {
	cfg := knowledge.DefaultConfig()
	var order []string
	mk := func(n string) spread.Hooks {
		return spread.Hooks{
			OnSpread:     func(spread.Result, error) { order = append(order, n+":spread") },
			OnSkip:       func(string) { order = append(order, n+":skip") },
			OnSpreadDone: func(time.Duration) { order = append(order, n+":done") },
		}
	}
	sp := mustSpreader(t, spread.DefaultConfig(), spread.WithHooks(mk("a")), spread.WithHooks(spread.Hooks{}), spread.WithHooks(mk("b")), spread.WithClock(stepClock(time.Second)))
	sp.Spread(nil, nil, 1)
	sp.Spread(newPerson("ana", cfg), nil, 1)
	want := "[a:skip b:skip a:spread b:spread a:done b:done]"
	if fmt.Sprint(order) != want {
		t.Fatalf("order %v, want %s", order, want)
	}
}
