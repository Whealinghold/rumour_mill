package knowledge

import (
	"math"
	"reflect"
	"testing"
)

type fadeCall struct {
	id           string
	now, elapsed Tick
}

// recorder returns a fader that records calls and applies f to the confidence.
func recorder(calls *[]fadeCall, f func(float64) float64) Fader {
	return func(x Fact, now, elapsed Tick) (float64, bool) {
		*calls = append(*calls, fadeCall{x.ID, now, elapsed})
		return f(x.Confidence), false
	}
}

func half(c float64) float64 { return c / 2 }

func addAt(t *testing.T, s *Store, id string, c float64, observed Tick) {
	t.Helper()
	if _, err := s.Add(Fact{ID: id, Type: "t", Subject: id, Source: "observed", Confidence: c, Observed: observed}); err != nil {
		t.Fatal(err)
	}
}

func TestFadeOrderAndChanged(t *testing.T) { // S6
	var calls []fadeCall
	s := mustStore(t, DefaultConfig(), WithFader(recorder(&calls, half)))
	for _, id := range []string{"b", "a", "c"} {
		addAt(t, s, id, 0.8, 0)
	}
	res := s.Fade(100)
	want := []fadeCall{{"a", 100, 100}, {"b", 100, 100}, {"c", 100, 100}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
	if !reflect.DeepEqual(res.Changed, []string{"a", "b", "c"}) || len(res.Forgotten) != 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestFadeComposition(t *testing.T) { // S7
	var calls []fadeCall
	s := mustStore(t, DefaultConfig(), WithFader(recorder(&calls, half)))
	addAt(t, s, "a", 0.8, 0)
	s.Fade(100)
	s.Fade(200)
	if calls[0].elapsed != 100 || calls[1].elapsed != 100 {
		t.Errorf("elapsed = %v, want 100 and 100", calls)
	}
	if f, _ := s.Get("a"); f.FadedAt != 200 {
		t.Errorf("FadedAt = %d, want 200", f.FadedAt)
	}

	// A fact added at 150 sees 50 at Fade(200).
	calls = nil
	s2 := mustStore(t, DefaultConfig(), WithFader(recorder(&calls, half)))
	addAt(t, s2, "late", 0.8, 150)
	s2.Fade(200)
	if calls[0].elapsed != 50 {
		t.Errorf("elapsed = %v, want 50", calls)
	}

	// A second Fade at the same tick sees elapsed 0.
	calls = nil
	s2.Fade(200)
	if len(calls) != 1 || calls[0].elapsed != 0 {
		t.Errorf("same-tick elapsed = %v, want 0", calls)
	}
}

func TestFadeSkipsExpired(t *testing.T) { // S8
	var calls []fadeCall
	s := mustStore(t, DefaultConfig(), WithFader(recorder(&calls, half)))
	exp := Tick(50)
	s.Add(Fact{ID: "e", Type: "t", Source: "observed", Confidence: 0.8, Expiry: &exp})
	res := s.Fade(100)
	if len(calls) != 0 || len(res.Changed) != 0 || len(res.Forgotten) != 0 {
		t.Errorf("expired fact touched: %v %+v", calls, res)
	}
	if f, ok := s.Get("e"); !ok || f.Confidence != 0.8 || f.FadedAt != 0 {
		t.Errorf("expired fact = %+v, %v", f, ok)
	}
	if s.Prune(100) != 1 {
		t.Error("Prune should remove it")
	}

	// A fact whose expiry equals now is already expired (boundary).
	calls = nil
	bound := Tick(100)
	s.Add(Fact{ID: "b", Type: "t", Source: "observed", Confidence: 0.8, Expiry: &bound})
	if s.Fade(100); len(calls) != 0 {
		t.Errorf("fact expiring at now was faded: %v", calls)
	}
}

func TestFadeForgets(t *testing.T) { // S9
	cfg := DefaultConfig()
	cfg.Fade.ForgetBelow = 0.15
	fader := func(f Fact, _, _ Tick) (float64, bool) {
		switch f.ID {
		case "forget":
			return 0.9, true
		case "low":
			return 0.1, false
		}
		return 0.5, false
	}
	s := mustStore(t, cfg, WithFader(fader))
	for _, id := range []string{"forget", "low", "keep"} {
		addAt(t, s, id, 0.8, 0)
	}
	res := s.Fade(10)
	if !reflect.DeepEqual(res.Forgotten, []string{"forget", "low"}) || !reflect.DeepEqual(res.Changed, []string{"keep"}) {
		t.Errorf("result = %+v", res)
	}
	for _, id := range []string{"forget", "low"} {
		if _, ok := s.Get(id); ok {
			t.Errorf("%s still present", id)
		}
	}
}

func TestFadeNaNAndClamp(t *testing.T) { // S10
	fader := func(f Fact, _, _ Tick) (float64, bool) {
		if f.ID == "nan" {
			return math.NaN(), false
		}
		return 1.4, false
	}
	s := mustStore(t, DefaultConfig(), WithFader(fader))
	addAt(t, s, "nan", 0.5, 0)
	addAt(t, s, "hi", 0.5, 0)
	res := s.Fade(10)
	if f, _ := s.Get("nan"); f.Confidence != 0.5 || f.FadedAt != 0 {
		t.Errorf("nan fact = %+v", f)
	}
	if f, _ := s.Get("hi"); f.Confidence != 1 || f.FadedAt != 10 {
		t.Errorf("hi fact = %+v", f)
	}
	if !reflect.DeepEqual(res.Changed, []string{"hi"}) {
		t.Errorf("Changed = %v", res.Changed)
	}
}

func TestFadeNoFader(t *testing.T) { // S11
	cfg := DefaultConfig()
	cfg.Fade.ForgetBelow = 0.9 // inert without a fader
	for name, s := range map[string]*Store{
		"default": mustStore(t, cfg),
		"nil":     mustStore(t, cfg, WithFader(nil)),
	} {
		addAt(t, s, "a", 0.5, 0)
		before := s.All()
		res := s.Fade(1000)
		if len(res.Changed) != 0 || len(res.Forgotten) != 0 || !reflect.DeepEqual(before, s.All()) {
			t.Errorf("%s: result %+v, store changed", name, res)
		}
	}
}

func TestFadeOnlyConfidenceAndFadedAtChange(t *testing.T) {
	s := mustStore(t, DefaultConfig(), WithFader(func(f Fact, _, _ Tick) (float64, bool) { return f.Confidence / 2, false }))
	exp := Tick(9999)
	s.Add(Fact{Type: "t", Subject: "x", Source: "observed", Confidence: 0.8, Observed: 5, Expiry: &exp,
		Payload: "p", Private: true, Prov: Provenance{From: "z", Hops: 2, EventID: "e", Premises: []string{"q"}}})
	before := s.All()[0]
	s.Fade(100)
	after := s.All()[0]
	if after.Confidence != 0.4 || after.FadedAt != 100 {
		t.Fatalf("after = %+v", after)
	}
	after.Confidence, after.FadedAt = before.Confidence, before.FadedAt
	if !reflect.DeepEqual(before, after) {
		t.Errorf("other fields changed: %+v -> %+v", before, after)
	}
}

func TestFadePrivatePassedAndFaded(t *testing.T) {
	var saw bool
	s := mustStore(t, DefaultConfig(), WithFader(func(f Fact, _, _ Tick) (float64, bool) { saw = f.Private; return 0.1, false }))
	s.Add(Fact{ID: "p", Type: "t", Source: "observed", Confidence: 0.8, Private: true})
	s.Fade(1)
	if f, _ := s.Get("p"); !saw || f.Confidence != 0.1 {
		t.Errorf("saw %v, fact %+v", saw, f)
	}
}

func TestFadeFaderGetsCopy(t *testing.T) {
	s := mustStore(t, DefaultConfig(), WithFader(func(f Fact, _, _ Tick) (float64, bool) {
		f.Prov.Premises[0] = "mutated"
		*f.Expiry = 1
		return f.Confidence, false
	}))
	exp := Tick(500)
	s.Add(Fact{ID: "a", Type: "t", Source: "observed", Confidence: 0.8, Expiry: &exp, Prov: Provenance{Premises: []string{"q"}}})
	s.Fade(10)
	f, _ := s.Get("a")
	if f.Prov.Premises[0] != "q" || *f.Expiry != 500 {
		t.Errorf("store aliased the fader's copy: %+v", f)
	}
}

func TestFadeElapsedBounds(t *testing.T) {
	tests := []struct {
		name     string
		observed Tick
		now      Tick
		want     Tick
	}{
		{"now before settled", 100, 50, 0},
		{"normal", 10, 25, 15},
		{"now equals settled", 100, 100, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls []fadeCall
			s := mustStore(t, DefaultConfig(), WithFader(recorder(&calls, half)))
			addAt(t, s, "a", 0.8, tc.observed)
			s.Fade(tc.now)
			if len(calls) != 1 || calls[0].elapsed != tc.want {
				t.Errorf("calls = %v, want elapsed %d", calls, tc.want)
			}
		})
	}
}

func TestFadeClampAndUnchanged(t *testing.T) {
	tests := []struct {
		name       string
		forgetBel  float64
		ret        float64
		wantStored float64
		forgotten  bool
		changed    bool
	}{
		{"lower clamp forgets when below ForgetBelow", 0.15, -1, 0, true, false},
		{"lower clamp stores 0 when not forgotten", 0, -1, 0, false, true},
		{"upper clamp stores 1", 0.15, 1.4, 1, false, true},
		{"unchanged survivor is not in Changed", 0.15, 0.5, 0.5, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Fade.ForgetBelow = tc.forgetBel
			s := mustStore(t, cfg, WithFader(func(Fact, Tick, Tick) (float64, bool) { return tc.ret, false }))
			addAt(t, s, "a", 0.5, 0)
			res := s.Fade(10)
			f, ok := s.Get("a")
			if ok == tc.forgotten {
				t.Fatalf("present = %v, want forgotten = %v", ok, tc.forgotten)
			}
			if got := len(res.Forgotten) == 1; got != tc.forgotten {
				t.Errorf("Forgotten = %v, want forgotten %v", res.Forgotten, tc.forgotten)
			}
			if got := len(res.Changed) == 1; got != tc.changed {
				t.Errorf("Changed = %v, want changed %v", res.Changed, tc.changed)
			}
			if ok && (f.Confidence != tc.wantStored || f.FadedAt != 10) { // survivors settle even when unchanged
				t.Errorf("fact = %+v, want confidence %v, FadedAt 10", f, tc.wantStored)
			}
		})
	}
}

func TestFadeDeterministicAndRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Fade.ForgetBelow = 0.08
	fader := func(f Fact, _, e Tick) (float64, bool) { return f.Confidence * math.Pow(0.5, float64(e)/100), false }
	s1 := mustStore(t, cfg, WithFader(fader))
	for i, id := range []string{"c", "a", "b", "d"} {
		addAt(t, s1, id, 0.9-0.2*float64(i), 0)
	}
	// Clone through All -> Add (also proves the round trip keeps everything).
	s2 := mustStore(t, cfg, WithFader(fader))
	for _, f := range s1.All() {
		if _, err := s2.Add(f); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(s1.All(), s2.All()) {
		t.Fatalf("clone differs: %+v vs %+v", s1.All(), s2.All())
	}
	for _, now := range []Tick{100, 250} {
		r1, r2 := s1.Fade(now), s2.Fade(now)
		if !reflect.DeepEqual(r1, r2) || !reflect.DeepEqual(s1.All(), s2.All()) {
			t.Errorf("now %d: results %+v vs %+v, stores %+v vs %+v", now, r1, r2, s1.All(), s2.All())
		}
	}
	if len(s1.All()) == 4 || len(s1.All()) == 0 {
		t.Error("expected some facts forgotten so Forgotten is exercised")
	}
	// Round trip of a faded store keeps FadedAt.
	s3 := mustStore(t, cfg)
	for _, f := range s1.All() {
		if _, err := s3.Add(f); err != nil {
			t.Fatal(err)
		}
	}
	if got := s3.All(); !reflect.DeepEqual(s1.All(), got) || got[0].FadedAt != 250 {
		t.Errorf("round trip lost data: %+v vs %+v", s1.All(), got)
	}
}

func TestFadeEmptyStore(t *testing.T) {
	s := mustStore(t, DefaultConfig(), WithFader(func(f Fact, _, _ Tick) (float64, bool) { return 0, true }))
	if res := s.Fade(10); len(res.Changed) != 0 || len(res.Forgotten) != 0 {
		t.Errorf("res = %+v", res)
	}
}

const tol = 1e-9 // math.Pow is stable per platform; compare with a tolerance

func harbourStore(t *testing.T) *Store {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Fade = FadeConfig{HalfLives: map[string]int64{"overheard": 100}, ForgetBelow: 0.15}
	s := mustStore(t, cfg)
	for id, src := range map[string]string{"o": "overheard", "w": "witnessed"} {
		if _, err := s.Add(Fact{ID: id, Type: "t", Subject: id, Source: src, Confidence: 0.8}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestBuiltinExponentialFade(t *testing.T) { // S12
	s := harbourStore(t)
	for _, tc := range []struct {
		now       Tick
		want      float64
		forgotten bool
	}{{100, 0.4, false}, {200, 0.2, false}, {300, 0, true}} {
		res := s.Fade(tc.now)
		o, ok := s.Get("o")
		if tc.forgotten {
			if ok || !reflect.DeepEqual(res.Forgotten, []string{"o"}) {
				t.Errorf("now %d: ok=%v result=%+v, want forgotten", tc.now, ok, res)
			}
		} else if !ok || math.Abs(o.Confidence-tc.want) > tol || !reflect.DeepEqual(res.Changed, []string{"o"}) {
			t.Errorf("now %d: %+v result=%+v, want %v", tc.now, o, res, tc.want)
		}
		if w, _ := s.Get("w"); w.Confidence != 0.8 {
			t.Errorf("now %d: witnessed = %v, want unchanged", tc.now, w.Confidence)
		}
	}
}

func TestBuiltinFadeComposes(t *testing.T) { // R11 (the PRD has no scenario number for composition; its S13 is the config table)
	a, b := harbourStore(t), harbourStore(t)
	a.Fade(100)
	a.Fade(200)
	b.Fade(200)
	fa, _ := a.Get("o")
	fb, _ := b.Get("o")
	if math.Abs(fa.Confidence-fb.Confidence) > tol {
		t.Errorf("stepwise %v != single %v", fa.Confidence, fb.Confidence)
	}
}

func TestFadeHalfLifeSelection(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Fade = FadeConfig{HalfLives: map[string]int64{"exempt": 0, "fast": 50}, DefaultHalfLife: 100}
	s := mustStore(t, cfg)
	for _, src := range []string{"exempt", "fast", "other"} {
		s.Add(Fact{ID: src, Type: "t", Subject: src, Source: src, Confidence: 1})
	}
	s.Fade(100)
	for src, want := range map[string]float64{"exempt": 1, "fast": 0.25, "other": 0.5} {
		if f, _ := s.Get(src); math.Abs(f.Confidence-want) > tol {
			t.Errorf("%s = %v, want %v", src, f.Confidence, want)
		}
	}
}

func TestFadeConfigOverrides(t *testing.T) {
	// Default: no fader, so nothing happens. Override each knob and behaviour changes.
	for name, mutate := range map[string]func(*Config){
		"half life":         func(c *Config) { c.Fade.HalfLives["observed"] = 10 },
		"default half life": func(c *Config) { c.Fade.DefaultHalfLife = 10 },
	} {
		cfg := DefaultConfig()
		s := mustStore(t, cfg)
		addAt(t, s, "a", 0.8, 0)
		if res := s.Fade(10); len(res.Changed) != 0 {
			t.Errorf("%s: default config faded: %+v", name, res)
		}
		cfg = DefaultConfig()
		mutate(&cfg)
		s = mustStore(t, cfg)
		addAt(t, s, "a", 0.8, 0)
		if res := s.Fade(10); len(res.Changed) != 1 {
			t.Errorf("%s: override did not fade: %+v", name, res)
		}
	}
	// ForgetBelow is inert alone (S11) and forgets once a policy exists.
	cfg := DefaultConfig()
	cfg.Fade.DefaultHalfLife, cfg.Fade.ForgetBelow = 10, 0.5
	s := mustStore(t, cfg)
	addAt(t, s, "a", 0.8, 0)
	if res := s.Fade(10); !reflect.DeepEqual(res.Forgotten, []string{"a"}) {
		t.Errorf("result = %+v", res)
	}
}

func TestWithFaderOverridesConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Fade.DefaultHalfLife = 10
	s := mustStore(t, cfg, WithFader(func(f Fact, _, _ Tick) (float64, bool) { return 0.123, false }))
	addAt(t, s, "a", 0.8, 0)
	s.Fade(10)
	if f, _ := s.Get("a"); f.Confidence != 0.123 {
		t.Errorf("confidence = %v, want custom fader's", f.Confidence)
	}
}

func TestStoreClonesHalfLives(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Fade.HalfLives["observed"] = 10
	s := mustStore(t, cfg)
	cfg.Fade.HalfLives["observed"] = 0 // must not reach the live store
	addAt(t, s, "a", 1, 0)
	s.Fade(10)
	if f, _ := s.Get("a"); math.Abs(f.Confidence-0.5) > tol {
		t.Errorf("confidence = %v, want 0.5", f.Confidence)
	}
	if a, b := DefaultConfig(), DefaultConfig(); reflect.ValueOf(a.Fade.HalfLives).Pointer() == reflect.ValueOf(b.Fade.HalfLives).Pointer() {
		t.Error("DefaultConfig shares HalfLives map")
	}
}

func TestBuiltinFadeExtremes(t *testing.T) {
	tests := []struct {
		name     string
		h        int64
		observed Tick
		now      Tick
		want     float64 // faded confidence from 0.9
	}{
		{"h=1 with saturated elapsed goes to zero", 1, math.MinInt64, math.MaxInt64, 0},
		{"h=MaxInt64 stays unchanged", math.MaxInt64, 0, 1000, 0.9},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Fade.DefaultHalfLife = tc.h
			s := mustStore(t, cfg)
			addAt(t, s, "a", 0.9, tc.observed)
			s.Fade(tc.now)
			f, ok := s.Get("a")
			if !ok || math.IsNaN(f.Confidence) || math.Abs(f.Confidence-tc.want) > tol {
				t.Errorf("got %+v ok=%v, want confidence %v", f, ok, tc.want)
			}
		})
	}
}

// R17: no positive half-life anywhere builds no fader, so ForgetBelow is inert.
func TestExponentialFaderNilWhenNoPositiveHalfLife(t *testing.T) {
	tests := []struct {
		name string
		cfg  FadeConfig
	}{
		{"zero entry, zero default", FadeConfig{HalfLives: map[string]int64{"a": 0}, ForgetBelow: 0.5}},
		{"empty", FadeConfig{ForgetBelow: 0.5}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if exponentialFader(tc.cfg) != nil {
				t.Error("fader should be nil")
			}
			cfg := DefaultConfig()
			cfg.Fade = tc.cfg
			s := mustStore(t, cfg)
			addAt(t, s, "a", 0.1, 0)
			if res := s.Fade(1000); len(res.Changed)+len(res.Forgotten) != 0 || len(s.All()) != 1 {
				t.Errorf("result = %+v, want nothing forgotten", res)
			}
		})
	}
}

func TestForgetBelowAloneChangesNothing(t *testing.T) { // P-D4, S11
	cfg := DefaultConfig()
	cfg.Fade.ForgetBelow = 0.9
	s := mustStore(t, cfg)
	addAt(t, s, "a", 0.1, 0)
	if res := s.Fade(1000); len(res.Changed)+len(res.Forgotten) != 0 {
		t.Errorf("result = %+v, want empty", res)
	}
	if _, ok := s.Get("a"); !ok {
		t.Error("fact forgotten without a fader")
	}
}

// R10: a Fade with an earlier now never moves FadedAt backwards.
func TestFadeNeverRewindsFadedAt(t *testing.T) {
	var calls []fadeCall
	s := mustStore(t, DefaultConfig(), WithFader(recorder(&calls, half)))
	addAt(t, s, "a", 0.8, 0)
	s.Fade(200)
	s.Fade(100)
	if f, _ := s.Get("a"); f.FadedAt != 200 {
		t.Errorf("FadedAt = %d, want 200", f.FadedAt)
	}
	if calls[1].elapsed != 0 {
		t.Errorf("elapsed = %d, want 0 for an earlier now", calls[1].elapsed)
	}
}

// R18: a later WithFader(nil) does not undo an earlier WithFader(f).
func TestWithFaderNilKeepsEarlierFader(t *testing.T) {
	s := mustStore(t, DefaultConfig(),
		WithFader(func(Fact, Tick, Tick) (float64, bool) { return 0.123, false }), WithFader(nil))
	addAt(t, s, "a", 0.8, 0)
	s.Fade(10)
	if f, _ := s.Get("a"); f.Confidence != 0.123 {
		t.Errorf("confidence = %v, want the earlier fader's", f.Confidence)
	}
}

// addNeg adds a fact whose Observed and FadedAt are both v, so max(Observed, FadedAt) stays negative.
func addNeg(t *testing.T, s *Store, id string, v Tick) {
	t.Helper()
	if _, err := s.Add(Fact{ID: id, Type: "t", Subject: id, Source: "observed", Confidence: 0.8, Observed: v, FadedAt: v}); err != nil {
		t.Fatal(err)
	}
}

// settled is hugely negative and now hugely positive, so now-settled overflows int64.
func TestFadeElapsedSaturatesOnRealOverflow(t *testing.T) {
	var calls []fadeCall
	s := mustStore(t, DefaultConfig(), WithFader(recorder(&calls, half)))
	addNeg(t, s, "a", math.MinInt64)
	s.Fade(math.MaxInt64)
	if len(calls) != 1 || calls[0].elapsed != math.MaxInt64 {
		t.Errorf("calls = %v, want elapsed MaxInt64", calls)
	}
	calls = nil
	s2 := mustStore(t, DefaultConfig(), WithFader(recorder(&calls, half)))
	addNeg(t, s2, "b", -1<<62)
	s2.Fade(math.MaxInt64)
	if len(calls) != 1 || calls[0].elapsed != math.MaxInt64 {
		t.Errorf("calls = %v, want elapsed MaxInt64", calls)
	}
}

// Examined and Invalid tell a dead or buggy fader from "nothing to change".
func TestFadeDiagnostics(t *testing.T) {
	nan := func(f Fact, _, _ Tick) (float64, bool) {
		if f.ID == "bad" {
			return math.NaN(), false
		}
		return f.Confidence, false
	}
	s := mustStore(t, DefaultConfig(), WithFader(nan))
	addAt(t, s, "bad", 0.5, 0)
	addAt(t, s, "ok", 0.5, 0)
	exp := Tick(5)
	if _, err := s.Add(Fact{ID: "old", Type: "t", Subject: "o", Source: "observed", Confidence: 0.5, Expiry: &exp}); err != nil {
		t.Fatal(err)
	}
	res := s.Fade(10)
	if res.Examined != 2 || !reflect.DeepEqual(res.Invalid, []string{"bad"}) || len(res.Changed) != 0 {
		t.Errorf("result = %+v, want Examined 2 (expired skipped), Invalid [bad], nothing changed", res)
	}

	// A config with no positive half-life builds no fader: Examined stays 0 although the store is not empty.
	off := mustStore(t, DefaultConfig())
	addAt(t, off, "a", 0.5, 0)
	if res := off.Fade(10); res.Examined != 0 || res.Invalid != nil {
		t.Errorf("no fader: result = %+v, want zero", res)
	}
}
