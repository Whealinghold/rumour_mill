package knowledge

import (
	"errors"
	"log/slog"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOnAddChainsInOrderAndIgnoresNil(t *testing.T) {
	var got []string
	s := mustStore(t, DefaultConfig(),
		WithHooks(Hooks{OnAdd: func(f Fact, r AddResult) { got = append(got, "1:"+f.ID+":"+r.ID) }}),
		WithHooks(Hooks{}), // nil fields
		WithHooks(Hooks{OnAdd: func(f Fact, r AddResult) { got = append(got, "2:"+r.Outcome.String()) }}),
	)
	if _, err := s.Add(Fact{Type: "t", Subject: "s", Source: "observed", Confidence: 1}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.HasPrefix(got[0], "1:t") || got[1] != "2:inserted" {
		t.Errorf("got %q", got)
	}
	// IDs equal on both sides, derived ID included.
	var f0 Fact
	var r0 AddResult
	s = mustStore(t, DefaultConfig(), WithHooks(Hooks{OnAdd: func(f Fact, r AddResult) { f0, r0 = f, r }}))
	s.Add(Fact{Type: "t", Subject: "s", Source: "observed", Confidence: 7})
	if f0.ID == "" || f0.ID != r0.ID || f0.Confidence != 1 {
		t.Errorf("hook fact %+v result %+v", f0, r0)
	}
}

func TestOnAddNotCalledOnError(t *testing.T) {
	calls := 0
	s := mustStore(t, DefaultConfig(), WithHooks(Hooks{OnAdd: func(Fact, AddResult) { calls++ }}))
	if _, err := s.Add(Fact{Source: "observed"}); !errors.Is(err, ErrInvalidFact) {
		t.Fatalf("err = %v", err)
	}
	s.Add(Fact{ID: "a", Type: "t", Subject: "s", Source: "observed"})
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if _, err := s.Add(Fact{ID: "a", Type: "t", Subject: "other", Source: "observed"}); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("err = %v", err)
	}
	if calls != 1 {
		t.Errorf("OnAdd ran on error: calls = %d", calls)
	}
}

func TestOnAddOutcomesAndCapOrder(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxFacts = 1
	var seen []AddResult
	var sizeInHook []int
	var s *Store
	s = mustStore(t, cfg, WithHooks(Hooks{OnAdd: func(_ Fact, r AddResult) {
		seen = append(seen, r)
		sizeInHook = append(sizeInHook, len(s.All())) // cap already enforced
	}}))
	s.Add(Fact{ID: "a", Type: "t", Source: "relayed", Confidence: 0.5})
	s.Add(Fact{ID: "b", Type: "t", Source: "observed", Confidence: 0.9})
	s.Add(Fact{ID: "c", Type: "t", Source: "derived", Confidence: 0.1}) // evicted itself: Rejected, still a successful Add
	var outs []string
	for _, r := range seen {
		outs = append(outs, r.Outcome.String())
	}
	if want := []string{"inserted", "inserted", "rejected"}; !reflect.DeepEqual(outs, want) {
		t.Errorf("outcomes = %v, want %v", outs, want)
	}
	if !reflect.DeepEqual(sizeInHook, []int{1, 1, 1}) {
		t.Errorf("store size inside hook = %v", sizeInHook)
	}
	if !reflect.DeepEqual(seen[1].Evicted, []string{"a"}) {
		t.Errorf("evicted = %v", seen[1].Evicted)
	}
}

func TestOnFadeChainsAndSkippedWithoutFader(t *testing.T) {
	var got []string
	hooks := []Option{
		WithHooks(Hooks{OnFade: func(r FadeResult) { got = append(got, "1") }}),
		WithHooks(Hooks{}),
		WithHooks(Hooks{OnFade: func(r FadeResult) { got = append(got, "2") }}),
	}
	s := mustStore(t, DefaultConfig(), hooks...)
	s.Fade(10) // no fader configured: hook still fires, unwired
	if !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Fatalf("OnFade without a Fader: %v", got)
	}
	got = nil
	var res FadeResult
	s = mustStore(t, DefaultConfig(), append(hooks, WithFader(half2), WithHooks(Hooks{OnFade: func(r FadeResult) { res = r }}))...)
	addAt(t, s, "a", 0.8, 0)
	s.Fade(10)
	if !reflect.DeepEqual(got, []string{"1", "2"}) || res.Examined != 1 || !reflect.DeepEqual(res.Changed, []string{"a"}) {
		t.Errorf("got %v res %+v", got, res)
	}
}

func half2(f Fact, _, _ Tick) (float64, bool) { return f.Confidence / 2, false }

func TestLoggerEvictionWarnAndFadeDebug(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxFacts = 1
	var sb strings.Builder
	l := slog.New(slog.NewTextHandler(&sb, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := mustStore(t, cfg, WithLogger(l), WithLogger(nil), WithFader(half2))
	addAt(t, s, "a", 0.5, 0)
	if sb.Len() != 0 {
		t.Fatalf("unexpected log on plain add: %s", sb.String())
	}
	s.Add(Fact{ID: "b", Type: "t", Source: "observed", Confidence: 0.9})
	out := sb.String()
	for _, w := range []string{"level=WARN", "fact=b", "outcome=inserted", "evicted=1"} {
		if !strings.Contains(out, w) {
			t.Errorf("warn log missing %q: %s", w, out)
		}
	}
	sb.Reset()
	s.Fade(10)
	out = sb.String()
	for _, w := range []string{"level=DEBUG", "examined=1", "changed=1", "forgotten=0", "invalid=0"} {
		if !strings.Contains(out, w) {
			t.Errorf("fade log missing %q: %s", w, out)
		}
	}
}

func TestDefaultLoggerSilent(t *testing.T) {
	old := slog.Default()
	var sb strings.Builder
	slog.SetDefault(slog.New(slog.NewTextHandler(&sb, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)
	cfg := DefaultConfig()
	cfg.MaxFacts = 1
	s := mustStore(t, cfg, WithFader(half2))
	addAt(t, s, "a", 0.5, 0)
	addAt(t, s, "b", 0.9, 0)
	s.Prune(5)
	s.Add(Fact{ID: "x", Type: "t", Source: "observed"})
	s.Add(Fact{ID: "x", Type: "t", Subject: "y", Source: "observed"})
	s.Fade(5)
	if sb.Len() != 0 {
		t.Errorf("default logger wrote: %s", sb.String())
	}
}

func TestOnPruneChainsNilAndCounts(t *testing.T) {
	var got []string
	s := mustStore(t, DefaultConfig(),
		WithHooks(Hooks{OnPrune: func(r PruneResult) { got = append(got, "1:"+strconv.Itoa(r.Removed)) }}),
		WithHooks(Hooks{}),
		WithHooks(Hooks{OnPrune: func(r PruneResult) { got = append(got, "2:"+strconv.Itoa(r.Removed)) }}),
	)
	s.Prune(5) // nothing to remove: still called, with 0
	exp := Tick(3)
	s.Add(Fact{ID: "a", Type: "t", Source: "observed", Expiry: &exp})
	s.Add(Fact{ID: "b", Type: "t", Source: "observed", Expiry: &exp})
	s.Add(Fact{ID: "c", Type: "t", Source: "observed"})
	if n := s.Prune(5); n != 2 {
		t.Fatalf("Prune = %d", n)
	}
	if want := []string{"1:0", "2:0", "1:2", "2:2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func nanFader(Fact, Tick, Tick) (float64, bool) { return math.NaN(), false }

func TestPruneDebugAndFadeInvalidWarnLevels(t *testing.T) {
	var sb strings.Builder
	l := slog.New(slog.NewTextHandler(&sb, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := mustStore(t, DefaultConfig(), WithLogger(l), WithFader(nanFader))
	exp := Tick(1)
	s.Add(Fact{ID: "a", Type: "t", Source: "observed", Expiry: &exp})
	sb.Reset()
	s.Prune(5)
	if out := sb.String(); !strings.Contains(out, "level=DEBUG") || !strings.Contains(out, "removed=1") || strings.Contains(out, "WARN") {
		t.Errorf("prune log: %s", out)
	}
	// A Warn-level logger sees only the Warn line, so the count asserted below
	// cannot be satisfied by the Debug summary, which also carries invalid=N.
	sb.Reset()
	s = mustStore(t, DefaultConfig(), WithLogger(slog.New(slog.NewTextHandler(&sb, &slog.HandlerOptions{Level: slog.LevelWarn}))), WithFader(nanFader))
	for i := range 7 {
		s.Add(Fact{ID: "f" + strconv.Itoa(i), Type: "t", Source: "observed"})
	}
	sb.Reset()
	s.Fade(10)
	out := sb.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "invalid=7") || !strings.Contains(out, "f4") || strings.Contains(out, "f5") {
		t.Errorf("fade warn (want 7 invalid, ids capped at 5): %s", out)
	}
	if n := strings.Count(out, "level=WARN"); n != 1 {
		t.Errorf("warns = %d, want once per Fade", n)
	}
}

func TestFadeNoWarnWhenValid(t *testing.T) {
	var sb strings.Builder
	l := slog.New(slog.NewTextHandler(&sb, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := mustStore(t, DefaultConfig(), WithLogger(l), WithFader(half2))
	addAt(t, s, "a", 0.8, 0)
	s.Fade(10)
	if strings.Contains(sb.String(), "WARN") {
		t.Errorf("unexpected warn: %s", sb.String())
	}
}

func TestIdentityMismatchWarnsBothErrors(t *testing.T) {
	var sb strings.Builder
	l := slog.New(slog.NewTextHandler(&sb, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := mustStore(t, DefaultConfig(), WithLogger(l))
	s.Add(Fact{ID: "a", Type: "t", Subject: "s", Source: "observed"})
	if _, err := s.Add(Fact{Source: "observed"}); !errors.Is(err, ErrInvalidFact) {
		t.Fatal(err)
	}
	if out := sb.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "invalid fact") {
		t.Errorf("ErrInvalidFact not logged: %s", out)
	}
	sb.Reset()
	if _, err := s.Add(Fact{ID: "a", Type: "t", Subject: "x", Source: "observed"}); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatal(err)
	}
	if out := sb.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "fact=a") {
		t.Errorf("mismatch log: %s", out)
	}
}

// stepClock returns a fake clock advancing step per read.
func stepClock(step time.Duration) func() time.Time {
	t := time.Unix(0, 0)
	return func() time.Time { t = t.Add(step); return t }
}

func TestLenAndSize(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxFacts = 2
	var adds []AddResult
	var fades []FadeResult
	var prunes []PruneResult
	s := mustStore(t, cfg, WithFader(half2), WithHooks(Hooks{
		OnAdd:   func(_ Fact, r AddResult) { adds = append(adds, r) },
		OnFade:  func(r FadeResult) { fades = append(fades, r) },
		OnPrune: func(r PruneResult) { prunes = append(prunes, r) },
	}))
	exp := Tick(1)
	s.Add(Fact{ID: "a", Type: "t", Source: "observed", Confidence: 1, Expiry: &exp})
	s.Add(Fact{ID: "a", Type: "t", Source: "observed", Confidence: 1, Expiry: &exp}) // merge: size unchanged
	s.Add(Fact{ID: "b", Type: "t", Source: "observed", Confidence: 1})
	s.Add(Fact{ID: "c", Type: "t", Source: "observed", Confidence: 1}) // evicts one
	if _, err := s.Add(Fact{}); err == nil {
		t.Fatal("want error")
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d", s.Len())
	}
	s.Fade(0)
	s.Prune(5)
	var sizes []int
	for _, r := range adds {
		sizes = append(sizes, r.Size)
	}
	if want := []int{1, 1, 2, 2}; !reflect.DeepEqual(sizes, want) {
		t.Errorf("add sizes %v, want %v", sizes, want)
	}
	if len(fades) != 1 || !fades[0].Wired || fades[0].Size != 2 {
		t.Errorf("fade %+v", fades)
	}
	if len(prunes) != 1 || prunes[0].Size != s.Len() || prunes[0].Removed != 2-s.Len() {
		t.Errorf("prune %+v len %d", prunes, s.Len())
	}
	if r, err := s.Add(Fact{}); err == nil || r.Size != 0 {
		t.Errorf("failed add size %d", r.Size)
	}
}

func TestUnwiredFade(t *testing.T) {
	var got FadeResult
	s := mustStore(t, DefaultConfig(), WithHooks(Hooks{OnFade: func(r FadeResult) { got = r }}))
	s.Add(Fact{ID: "a", Type: "t", Source: "observed"})
	s.Fade(1)
	if !reflect.DeepEqual(got, FadeResult{Size: 1}) {
		t.Errorf("got %+v", got)
	}
}

func TestOnAddError(t *testing.T) {
	type call struct{ id, reason string }
	for _, tc := range []struct {
		name   string
		fact   Fact
		reason string
	}{
		{"invalid", Fact{Source: "observed", Confidence: 5}, AddErrorInvalid},
		{"identity mismatch", Fact{ID: "a", Type: "t", Subject: "x", Source: "observed"}, AddErrorIdentityMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []call
			var gotFact Fact
			var adds int
			s := mustStore(t, DefaultConfig(), WithHooks(Hooks{
				OnAdd:      func(Fact, AddResult) { adds++ },
				OnAddError: func(f Fact, reason string, err error) { got = append(got, call{f.ID, reason}); gotFact = f },
			}))
			s.Add(Fact{ID: "a", Type: "t", Subject: "s", Source: "observed"})
			if _, err := s.Add(tc.fact); err == nil {
				t.Fatal("want error")
			}
			if want := []call{{tc.fact.ID, tc.reason}}; !reflect.DeepEqual(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
			if gotFact.Confidence != tc.fact.Confidence || adds != 1 {
				t.Errorf("fact normalised or OnAdd ran: %+v adds=%d", gotFact, adds)
			}
		})
	}
	if want := []string{"invalid", "identity_mismatch"}; !reflect.DeepEqual(AddErrorReasons(), want) {
		t.Errorf("reasons %v", AddErrorReasons())
	}
}

func TestOnDurationClock(t *testing.T) {
	type obs struct {
		op string
		d  time.Duration
	}
	run := func(opts ...Option) []obs {
		var got []obs
		opts = append(opts, WithFader(half2), WithHooks(Hooks{OnDuration: func(op string, d time.Duration) { got = append(got, obs{op, d}) }}))
		s := mustStore(t, DefaultConfig(), opts...)
		s.Find(Query{}, 0)
		s.Fade(0)
		s.Prune(0)
		return got
	}
	if got := run(WithClock(nil)); got != nil {
		t.Errorf("no clock: %v", got)
	}
	want := []obs{{OpFind, time.Second}, {OpFade, time.Second}, {OpPrune, time.Second}}
	if got := run(WithClock(stepClock(time.Second))); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	back := time.Unix(100, 0)
	if got := run(WithClock(func() time.Time { back = back.Add(-time.Second); return back })); got[0].d != 0 {
		t.Errorf("negative not floored: %v", got)
	}
}
