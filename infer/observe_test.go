package infer_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
)

type runEvent struct {
	res infer.Result
	err error
}

func recorder(log *[]string, name string, ev *[]runEvent) infer.Hooks {
	return infer.Hooks{OnRun: func(r infer.Result, err error) {
		*log = append(*log, name)
		if ev != nil {
			*ev = append(*ev, runEvent{r, err})
		}
	}}
}

func TestHooksChainInOrder(t *testing.T) {
	var order []string
	var evs []runEvent
	e, err := infer.New(infer.DefaultConfig(), []infer.Rule{simple("r", "a", "b", 0.5)},
		infer.WithHooks(recorder(&order, "first", nil)),
		infer.WithHooks(infer.Hooks{}), // nil OnRun ignored
		nil,                            // nil option ignored
		infer.WithHooks(recorder(&order, "second", &evs)),
	)
	if err != nil {
		t.Fatal(err)
	}
	s := store(t)
	put(t, s, "a", "x", 0.9)
	res := run(t, e, s, 0)
	if strings.Join(order, ",") != "first,second" {
		t.Errorf("order = %v, want first,second", order)
	}
	if len(evs) != 1 || evs[0].err != nil || len(evs[0].res.Derived) != 1 || evs[0].res.Passes != res.Passes {
		t.Errorf("hook saw %+v, want the Run result and nil error", evs)
	}
}

func TestNoOptionsIsNoOp(t *testing.T) {
	e := engine(t, infer.DefaultConfig(), simple("r", "a", "b", 0.5))
	s := store(t)
	put(t, s, "a", "x", 0.9)
	if res := run(t, e, s, 0); len(res.Derived) != 1 {
		t.Errorf("derived %d, want 1", len(res.Derived))
	}
	// A nil logger keeps the discarding default rather than panicking.
	e2, _ := infer.New(infer.DefaultConfig(), nil, infer.WithLogger(nil))
	if _, err := e2.Run(store(t), 0); err != nil {
		t.Fatal(err)
	}
}

func TestOnRunErrorPaths(t *testing.T) {
	bad := simple("r", "a", "b\x1f", 0.5) // the store rejects the separator in a type
	tests := []struct {
		name    string
		store   func() *knowledge.Store
		rules   []infer.Rule
		wantErr error
	}{
		{"nil store", func() *knowledge.Store { return nil }, nil, infer.ErrNilStore},
		{"store rejects derived fact", func() *knowledge.Store {
			s := store(t)
			put(t, s, "a", "x", 0.9)
			return s
		}, []infer.Rule{bad}, knowledge.ErrInvalidFact},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var evs []runEvent
			var order []string
			e, err := infer.New(infer.DefaultConfig(), tc.rules, infer.WithHooks(recorder(&order, "h", &evs)))
			if err != nil {
				t.Fatal(err)
			}
			_, runErr := e.Run(tc.store(), 0)
			if !errors.Is(runErr, tc.wantErr) {
				t.Fatalf("Run err = %v, want %v", runErr, tc.wantErr)
			}
			if len(evs) != 1 || !errors.Is(evs[0].err, tc.wantErr) {
				t.Errorf("hook saw %+v, want one call carrying %v", evs, tc.wantErr)
			}
		})
	}
}

func TestOnRunMaxPassesExhaustion(t *testing.T) {
	cfg := infer.DefaultConfig() // one pass; the chain needs three
	var evs []runEvent
	var order []string
	e, _ := infer.New(cfg, []infer.Rule{simple("r1", "a", "b", 0.5), simple("r2", "b", "c", 0.5)},
		infer.WithHooks(recorder(&order, "h", &evs)))
	s := store(t)
	put(t, s, "a", "x", 0.9)
	run(t, e, s, 0)
	if len(evs) != 1 || evs[0].err != nil || evs[0].res.Passes != 1 || len(evs[0].res.Derived) != 1 {
		t.Errorf("hook saw %+v, want nil error, 1 pass, 1 derived", evs)
	}
}

func TestLogger(t *testing.T) {
	tests := []struct {
		name      string
		withStore bool
		rules     []infer.Rule
		want      []string
		wantNot   string
	}{
		{"debug summary", true, []infer.Rule{simple("r", "a", "b", 0.5)}, []string{"level=DEBUG", "passes=1", "derived=1"}, "WARN"},
		{"warn on error", false, nil, []string{"level=WARN", "err="}, "DEBUG"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
			e, err := infer.New(infer.DefaultConfig(), tc.rules, infer.WithLogger(slog.New(h)))
			if err != nil {
				t.Fatal(err)
			}
			var s *knowledge.Store
			if tc.withStore {
				s = store(t)
				put(t, s, "a", "x", 0.9)
			}
			e.Run(s, 0) //nolint:errcheck // the error is the point of one case
			out := buf.String()
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("log %q lacks %q", out, w)
				}
			}
			if strings.Contains(out, tc.wantNot) {
				t.Errorf("log %q should not contain %q", out, tc.wantNot)
			}
		})
	}
}

func TestObservationDoesNotChangeResult(t *testing.T) {
	rules := []infer.Rule{simple("r1", "a", "b", 0.5), simple("r2", "b", "c", 0.5)}
	cfg := infer.DefaultConfig()
	cfg.MaxPasses = 3
	plain := engine(t, cfg, rules...)
	watched, _ := infer.New(cfg, rules, infer.WithHooks(infer.Hooks{OnRun: func(infer.Result, error) {}}),
		infer.WithLogger(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))))
	s1, s2 := store(t), store(t)
	put(t, s1, "a", "x", 0.9)
	put(t, s2, "a", "x", 0.9)
	a, b := run(t, plain, s1, 0), run(t, watched, s2, 0)
	if strings.Join(ids(a), ",") != strings.Join(ids(b), ",") || a.Passes != b.Passes {
		t.Errorf("observed run differs: %v/%d vs %v/%d", ids(a), a.Passes, ids(b), b.Passes)
	}
}

// stepClock returns a clock advancing by step on every read, and a read counter.
func stepClock(step time.Duration) (func() time.Time, *int) {
	t := time.Unix(0, 0)
	reads := 0
	return func() time.Time { reads++; t = t.Add(step); return t }, &reads
}

func TestTruncated(t *testing.T) {
	chain := []infer.Rule{simple("r1", "a", "b", 0.5), simple("r2", "b", "c", 0.5)} // needs 3 passes
	tests := []struct {
		name      string
		maxPasses int
		want      bool
		passes    int
	}{
		{"one pass never truncated", 1, false, 1},
		{"two passes cut a chain", 2, true, 2},
		{"enough passes", 3, false, 3},
		{"plenty of passes", 5, false, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := infer.DefaultConfig()
			cfg.MaxPasses = tc.maxPasses
			var buf bytes.Buffer
			h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
			var evs []runEvent
			var order []string
			e, err := infer.New(cfg, chain, infer.WithLogger(slog.New(h)), infer.WithHooks(recorder(&order, "h", &evs)))
			if err != nil {
				t.Fatal(err)
			}
			s := store(t)
			put(t, s, "a", "x", 0.9)
			res := run(t, e, s, 0)
			if res.Truncated != tc.want || res.Passes != tc.passes || evs[0].res.Truncated != tc.want {
				t.Errorf("Truncated=%v Passes=%d, want %v/%d", res.Truncated, res.Passes, tc.want, tc.passes)
			}
			if got := strings.Contains(buf.String(), "level=WARN msg=\"infer run truncated\""); got != tc.want {
				t.Errorf("truncation warn = %v, want %v: %s", got, tc.want, buf.String())
			}
			if !strings.Contains(buf.String(), "truncated=") {
				t.Errorf("debug line lacks truncated attr: %s", buf.String())
			}
		})
	}
}

func TestByRuleLog(t *testing.T) {
	cfg := infer.DefaultConfig()
	cfg.MaxPasses = 3
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	e, _ := infer.New(cfg, []infer.Rule{simple("r1", "a", "b", 0.5), simple("r2", "b", "c", 0.5), simple("idle", "z", "y", 0.5)},
		infer.WithLogger(slog.New(h)))
	s := store(t)
	put(t, s, "a", "x", 0.9)
	run(t, e, s, 0)
	out := buf.String()
	if !strings.Contains(out, `by_rule="map[r1:1 r2:1]"`) {
		t.Errorf("log %q lacks by_rule map[r1:1 r2:1] (idle rule must be omitted)", out)
	}
}

func TestClockAndOnRunDone(t *testing.T) {
	tests := []struct {
		name      string
		clock     bool
		nilStore  bool
		wantCalls int
	}{
		{"no clock never fires", false, false, 0},
		{"clock times a run", true, false, 1},
		{"clock times the nil-store call", true, true, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock, reads := stepClock(3 * time.Millisecond)
			var done []time.Duration
			var order []string
			opts := []infer.Option{
				infer.WithHooks(infer.Hooks{OnRunDone: func(d time.Duration) { order = append(order, "first"); done = append(done, d) }}),
				infer.WithHooks(infer.Hooks{OnRunDone: func(time.Duration) { order = append(order, "second") }}),
				infer.WithClock(nil), // ignored
			}
			if tc.clock {
				opts = append(opts, infer.WithClock(clock))
			}
			e, _ := infer.New(infer.DefaultConfig(), []infer.Rule{simple("r", "a", "b", 0.5)}, opts...)
			s := store(t)
			if tc.nilStore {
				s = nil
			} else {
				put(t, s, "a", "x", 0.9)
			}
			e.Run(s, 0) //nolint:errcheck // nil-store case errors by design
			if len(done) != tc.wantCalls {
				t.Fatalf("OnRunDone calls = %d, want %d", len(done), tc.wantCalls)
			}
			if tc.wantCalls == 0 {
				if *reads != 0 {
					t.Errorf("clock read %d times without WithClock", *reads)
				}
				return
			}
			if *reads != 2 || done[0] != 3*time.Millisecond || strings.Join(order, ",") != "first,second" {
				t.Errorf("reads=%d d=%v order=%v, want 2 reads, 3ms, first,second", *reads, done, order)
			}
		})
	}
}

func TestClockGoingBackwardsFloorsAtZero(t *testing.T) {
	ts := []time.Time{time.Unix(10, 0), time.Unix(5, 0)}
	i := 0
	var got time.Duration = -1
	e, _ := infer.New(infer.DefaultConfig(), nil,
		infer.WithClock(func() time.Time { i++; return ts[i-1] }),
		infer.WithHooks(infer.Hooks{OnRunDone: func(d time.Duration) { got = d }}))
	if _, err := e.Run(store(t), 0); err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("d = %v, want 0", got)
	}
}
