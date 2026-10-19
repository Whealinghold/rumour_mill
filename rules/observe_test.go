package rules_test

import (
	"bytes"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Whealinghold/rumour_mill/rules"
)

func failRule(id string, stop rules.StopMode) rules.Rule[*tctx] {
	return rules.Rule[*tctx]{ID: id, Event: "ev", Stop: stop, Do: func(*tctx) error { return errors.New("boom") }}
}

func TestObserveChainsInOrderAndIgnoresNil(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*L")
	mustRegister(t, r, "L", "", logRule("a", 0, rules.StopNone))
	var order []string
	r.Observe(rules.Hooks{}) // nil OnProcess ignored
	r.Observe(rules.Hooks{OnProcess: func(rules.Result, error) { order = append(order, "first") }})
	r.Observe(rules.Hooks{OnProcess: func(rules.Result, error) { order = append(order, "second") }})
	process(t, r, &tctx{})
	if !slices.Equal(order, []string{"first", "second"}) {
		t.Fatalf("order = %v", order)
	}
}

func TestOnProcessReceivesResultAndError(t *testing.T) {
	tests := []struct {
		name      string
		cfg       rules.Config
		rules     []rules.Rule[*tctx]
		wantFired int
		wantErr   bool
		wantStop  rules.StopMode
	}{
		{"no rules", rules.DefaultConfig(), nil, 0, false, rules.StopNone},
		{"fired", rules.DefaultConfig(), []rules.Rule[*tctx]{logRule("a", 0, rules.StopAll)}, 1, false, rules.StopAll},
		{"continue on error", rules.DefaultConfig(), []rules.Rule[*tctx]{failRule("f", rules.StopNone), logRule("b", 1, rules.StopNone)}, 2, true, rules.StopNone},
		{"stop on error early return", rules.Config{OnActionError: rules.StopOnError}, []rules.Rule[*tctx]{failRule("f", rules.StopNone), logRule("b", 1, rules.StopNone)}, 1, true, rules.StopNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newReg(t, tc.cfg, "*L")
			for _, ru := range tc.rules {
				mustRegister(t, r, "L", "", ru)
			}
			calls := 0
			var gotRes rules.Result
			var gotErr error
			r.Observe(rules.Hooks{OnProcess: func(res rules.Result, err error) { calls++; gotRes, gotErr = res, err }})
			res, err := r.Process(&tctx{}, "ev")
			if calls != 1 {
				t.Fatalf("hook calls = %d, want 1", calls)
			}
			if len(gotRes.Fired) != tc.wantFired || gotRes.Stopped != tc.wantStop || (gotErr != nil) != tc.wantErr {
				t.Fatalf("hook got %+v, %v", gotRes, gotErr)
			}
			if !slices.Equal(gotRes.Fired, res.Fired) || gotErr != err {
				t.Fatalf("hook saw different values than Process returned")
			}
		})
	}
}

func TestObserveAppliesFromNextProcess(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*L")
	process(t, r, &tctx{})
	n := 0
	r.Observe(rules.Hooks{OnProcess: func(rules.Result, error) { n++ }})
	if n != 0 {
		t.Fatal("hook ran before Process")
	}
	process(t, r, &tctx{})
	if n != 1 {
		t.Fatalf("n = %d", n)
	}
}

func TestSetLoggerLines(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := newReg(t, rules.DefaultConfig(), "*L")
	r.SetLogger(lg)
	r.SetLogger(nil) // ignored: logger stays
	mustRegister(t, r, "L", "", logRule("a", 0, rules.StopAll))
	process(t, r, &tctx{})
	out := buf.String()
	for _, want := range []string{"level=DEBUG", "event=ev", "fired=1", "stopped=all"} {
		if !strings.Contains(out, want) {
			t.Errorf("debug log missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "level=WARN") {
		t.Errorf("unexpected warn: %s", out)
	}

	buf.Reset()
	r2 := newReg(t, rules.DefaultConfig(), "*L")
	r2.SetLogger(lg)
	mustRegister(t, r2, "L", "", failRule("f", rules.StopNone))
	if _, err := r2.Process(&tctx{}, "ev"); err == nil {
		t.Fatal("want error")
	}
	out = buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "boom") {
		t.Errorf("warn missing: %s", out)
	}
}

func TestDefaultLoggerIsSilent(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*L")
	mustRegister(t, r, "L", "", failRule("f", rules.StopNone))
	_, _ = r.Process(&tctx{}, "ev") // must not panic with the default logger
}

func TestObserveSetLoggerConcurrentWithProcess(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*L")
	mustRegister(t, r, "L", "", rules.Rule[*tctx]{ID: "a", Event: "ev"})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for range 100 {
				_, _ = r.Process(&tctx{}, "ev")
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				r.Observe(rules.Hooks{OnProcess: func(rules.Result, error) {}})
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				r.SetLogger(slog.New(slog.DiscardHandler))
			}
		}()
	}
	wg.Wait()
}

func TestRegisterUnregisterLogLines(t *testing.T) {
	var buf bytes.Buffer
	r := newReg(t, rules.DefaultConfig(), "*L")
	r.SetLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	take := func() string { s := buf.String(); buf.Reset(); return s }
	has := func(t *testing.T, out string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("log missing %q: %s", w, out)
			}
		}
	}

	mustRegister(t, r, "L", "", logRule("a", 0, rules.StopNone))
	has(t, take(), "level=DEBUG", "layer=L", "rule=a", "ok=true", "registered")

	if err := r.Register("L", "", logRule("a", 0, rules.StopNone)); !errors.Is(err, rules.ErrDuplicateRule) {
		t.Fatalf("err = %v", err)
	}
	if out := take(); out != "" {
		t.Errorf("failed Register must not log, got %q", out)
	}

	if !r.Unregister("L", "", "a") {
		t.Fatal("Unregister(a) = false")
	}
	has(t, take(), "level=DEBUG", "unregistered", "rule=a", "ok=true")

	if r.Unregister("L", "", "a") {
		t.Fatal("second Unregister = true")
	}
	has(t, take(), "level=DEBUG", "unregistered", "rule=a", "ok=false")
}

func TestLayersAndRuleCount(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*A", "B")
	got := r.Layers()
	if !slices.Equal(got, []string{"A", "B"}) {
		t.Fatalf("Layers = %v", got)
	}
	got[0] = "x" // fresh copy
	if r.Layers()[0] != "A" {
		t.Fatal("Layers not a copy")
	}
	mustRegister(t, r, "A", "", logRule("a1", 0, rules.StopNone))
	mustRegister(t, r, "B", "s1", logRule("b1", 0, rules.StopNone))
	mustRegister(t, r, "B", "s2", logRule("b2", 0, rules.StopNone))
	if err := r.Register("B", "s2", logRule("b2", 0, rules.StopNone)); err == nil {
		t.Fatal("want duplicate")
	}
	tests := []struct {
		layer string
		want  int
	}{{"A", 1}, {"B", 2}, {"nope", 0}}
	for _, tc := range tests {
		if n := r.RuleCount(tc.layer); n != tc.want {
			t.Errorf("RuleCount(%s) = %d, want %d", tc.layer, n, tc.want)
		}
	}
	r.Unregister("B", "s1", "b1")
	r.Unregister("B", "s1", "b1") // not found: count unchanged
	if n := r.RuleCount("B"); n != 1 {
		t.Errorf("after unregister RuleCount(B) = %d", n)
	}
	empty, err := rules.NewRegistry[*tctx](rules.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	res, err := empty.Process(&tctx{}, "ev")
	if err != nil || len(res.Layers) != 0 || !res.Unmatched() || len(empty.Layers()) != 0 {
		t.Fatalf("empty registry: %+v %v", res, err)
	}
}

func TestResultLayerStats(t *testing.T) {
	no := func(*tctx) bool { return false }
	tests := []struct {
		name      string
		cfg       rules.Config
		rules     map[string][]rules.Rule[*tctx] // layer -> rules
		event     string
		want      []rules.LayerStats
		unmatched bool
		failed    []bool
	}{
		{"typo event", rules.DefaultConfig(), map[string][]rules.Rule[*tctx]{"A": {logRule("a", 0, rules.StopNone)}}, "typo",
			[]rules.LayerStats{{Layer: "A"}, {Layer: "B"}}, true, nil},
		{"skipped by when", rules.DefaultConfig(), map[string][]rules.Rule[*tctx]{
			"A": {{ID: "w", Event: "ev", When: no}, logRule("a", 1, rules.StopNone)}}, "ev",
			[]rules.LayerStats{{Layer: "A", Candidates: 2, Skipped: 1}, {Layer: "B"}}, false, []bool{false}},
		{"failures continue", rules.DefaultConfig(), map[string][]rules.Rule[*tctx]{
			"A": {failRule("f1", rules.StopNone), {ID: "f2", Event: "ev", Priority: 1, Do: failRule("", rules.StopNone).Do}}}, "ev",
			[]rules.LayerStats{{Layer: "A", Candidates: 2, Failed: 2}, {Layer: "B"}}, false, []bool{true, true}},
		{"stop on error ends in layer", rules.Config{OnActionError: rules.StopOnError}, map[string][]rules.Rule[*tctx]{
			"A": {failRule("f1", rules.StopNone), logRule("a", 1, rules.StopNone)}, "B": {logRule("b", 0, rules.StopNone)}}, "ev",
			[]rules.LayerStats{{Layer: "A", Candidates: 2, Failed: 1}}, false, []bool{true}},
		{"stop all ends in layer", rules.DefaultConfig(), map[string][]rules.Rule[*tctx]{
			"A": {logRule("a", 0, rules.StopAll)}, "B": {logRule("b", 0, rules.StopNone)}}, "ev",
			[]rules.LayerStats{{Layer: "A", Candidates: 1}}, false, []bool{false}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newReg(t, tc.cfg, "*A", "*B")
			for layer, rs := range tc.rules {
				for _, ru := range rs {
					mustRegister(t, r, layer, "", ru)
				}
			}
			res, _ := r.Process(&tctx{}, tc.event)
			if !slices.Equal(res.Layers, tc.want) {
				t.Fatalf("Layers = %+v, want %+v", res.Layers, tc.want)
			}
			if res.Unmatched() != tc.unmatched {
				t.Errorf("Unmatched = %v", res.Unmatched())
			}
			if tc.failed != nil {
				var got []bool
				for _, f := range res.Fired {
					got = append(got, f.Failed)
				}
				if !slices.Equal(got, tc.failed) {
					t.Errorf("Failed flags = %v, want %v", got, tc.failed)
				}
			}
		})
	}
}

func TestRegisterUnregisterHooks(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*A")
	var regs []rules.RegisterEvent
	var unregs []rules.UnregisterEvent
	r.Observe(rules.Hooks{OnRegister: func(e rules.RegisterEvent) { regs = append(regs, e) }})
	r.Observe(rules.Hooks{OnUnregister: func(e rules.UnregisterEvent) { unregs = append(unregs, e) }})
	_ = r.Register("A", "", logRule("a", 0, rules.StopNone))
	_ = r.Register("A", "", logRule("a", 0, rules.StopNone))       // duplicate
	_ = r.Register("A", "", rules.Rule[*tctx]{})                   // invalid
	_ = r.Register("nope", "", logRule("x", 0, rules.StopNone))    // unknown layer
	_ = r.Register("A", "scoped", logRule("y", 0, rules.StopNone)) // invalid scope
	r.Unregister("A", "", "a")
	r.Unregister("A", "", "a")
	wantErr := []error{nil, rules.ErrDuplicateRule, rules.ErrInvalidRule, rules.ErrUnknownLayer, rules.ErrInvalidRule}
	wantCount := []int{1, 1, 1, 0, 1}
	if len(regs) != 5 {
		t.Fatalf("register events = %d", len(regs))
	}
	for i, e := range regs {
		if !errors.Is(e.Err, wantErr[i]) || (wantErr[i] == nil && e.Err != nil) || e.Count != wantCount[i] {
			t.Errorf("reg[%d] = %+v", i, e)
		}
	}
	if regs[0].Layer != "A" || regs[0].RuleID != "a" {
		t.Errorf("reg[0] = %+v", regs[0])
	}
	if len(unregs) != 2 || !unregs[0].Removed || unregs[0].Count != 0 || unregs[1].Removed || unregs[1].RuleID != "a" {
		t.Errorf("unregs = %+v", unregs)
	}
}

// stepClock returns a clock advancing by step on every read.
func stepClock(step time.Duration) (func() time.Time, *int) {
	reads := 0
	t0 := time.Unix(0, 0)
	return func() time.Time { reads++; return t0.Add(time.Duration(reads-1) * step) }, &reads
}

func TestSetClockAndProcessDone(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*A")
	var order []string
	var durs []time.Duration
	r.Observe(rules.Hooks{
		OnProcess:     func(rules.Result, error) { order = append(order, "process") },
		OnProcessDone: func(d time.Duration) { order = append(order, "done"); durs = append(durs, d) },
	})
	process(t, r, &tctx{}) // no clock: never fires
	if len(durs) != 0 {
		t.Fatal("OnProcessDone fired without clock")
	}
	clock, reads := stepClock(3 * time.Millisecond)
	r.SetClock(clock)
	process(t, r, &tctx{})
	if *reads != 2 || len(durs) != 1 || durs[0] != 3*time.Millisecond {
		t.Fatalf("reads=%d durs=%v", *reads, durs)
	}
	if !slices.Equal(order, []string{"process", "process", "done"}) {
		t.Fatalf("order = %v", order)
	}
	r.SetClock(func() time.Time { return time.Unix(0, 0) }) // equal reads: 0
	back := time.Unix(100, 0)
	n := 0
	r.SetClock(func() time.Time { n++; return back.Add(-time.Duration(n) * time.Second) }) // backwards: floored
	process(t, r, &tctx{})
	if durs[1] != 0 {
		t.Fatalf("negative duration not floored: %v", durs[1])
	}
	r.SetClock(nil)
	process(t, r, &tctx{})
	if len(durs) != 2 {
		t.Fatal("nil clock must clear")
	}
}

func TestDebugLogIncludesFiredIDs(t *testing.T) {
	var buf bytes.Buffer
	r := newReg(t, rules.DefaultConfig(), "*L")
	r.SetLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	mustRegister(t, r, "L", "", logRule("a", 0, rules.StopNone))
	mustRegister(t, r, "L", "", rules.Rule[*tctx]{ID: "w", Event: "ev", Priority: 1, When: func(*tctx) bool { return false }})
	process(t, r, &tctx{})
	out := buf.String()
	for _, want := range []string{"L//a", "considered=2", "skipped=1", "failed=0", "unmatched=false"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q: %s", want, out)
		}
	}
	buf.Reset()
	_, _ = r.Process(&tctx{}, "typo")
	if !strings.Contains(buf.String(), "unmatched=true") {
		t.Errorf("log: %s", buf.String())
	}
}

func TestRegistryHooksConcurrent(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*L")
	clock, _ := stepClock(time.Microsecond)
	var mu sync.Mutex
	r.SetClock(func() time.Time { mu.Lock(); defer mu.Unlock(); return clock() })
	r.Observe(rules.Hooks{OnRegister: func(rules.RegisterEvent) {}, OnUnregister: func(rules.UnregisterEvent) {}, OnProcessDone: func(time.Duration) {}})
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := strconv.Itoa(i)
			for range 100 {
				_ = r.Register("L", "", rules.Rule[*tctx]{ID: id, Event: "ev"})
				_, _ = r.Process(&tctx{}, "ev")
				_ = r.RuleCount("L")
				r.Unregister("L", "", id)
			}
		}()
	}
	wg.Wait()
	if n := r.RuleCount("L"); n != 0 {
		t.Fatalf("RuleCount = %d", n)
	}
}
