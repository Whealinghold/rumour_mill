package rules_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Whealinghold/rumour_mill/logic"
	"github.com/Whealinghold/rumour_mill/rules"
)

// tctx is a minimal caller context: which scope keys it has per layer, and a log.
type tctx struct {
	scopes map[string][]string
	log    []string
}

type reg = rules.Registry[*tctx]

// newReg builds a registry whose layers are named in order; a name starting
// with "*" is a universal layer (nil Scopes), others read c.scopes[name].
func newReg(t *testing.T, cfg rules.Config, names ...string) *reg {
	t.Helper()
	var layers []rules.Layer[*tctx]
	for _, n := range names {
		if strings.HasPrefix(n, "*") {
			layers = append(layers, rules.Layer[*tctx]{Name: n[1:]})
			continue
		}
		layers = append(layers, rules.Layer[*tctx]{Name: n, Scopes: func(c *tctx) []string { return c.scopes[n] }})
	}
	r, err := rules.NewRegistry(cfg, layers...)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

// logRule is a rule that appends its ID to the context log.
func logRule(id string, priority int, stop rules.StopMode) rules.Rule[*tctx] {
	return rules.Rule[*tctx]{
		ID: id, Event: "ev", Priority: priority, Stop: stop,
		Do: func(c *tctx) error { c.log = append(c.log, id); return nil },
	}
}

func mustRegister(t *testing.T, r *reg, layer, scope string, rule rules.Rule[*tctx]) {
	t.Helper()
	if err := r.Register(layer, scope, rule); err != nil {
		t.Fatalf("Register %s/%s/%s: %v", layer, scope, rule.ID, err)
	}
}

func process(t *testing.T, r *reg, c *tctx) rules.Result {
	t.Helper()
	res, err := r.Process(c, "ev")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return res
}

func TestPriorityOrder(t *testing.T) {
	type rr struct {
		id   string
		prio int
	}
	tests := []struct {
		name string
		regs []rr
		want []string
	}{
		{"lower number runs first", []rr{{"c", 30}, {"a", 10}, {"b", 20}}, []string{"a", "b", "c"}},
		{"ties run in registration order", []rr{{"x", 5}, {"y", 5}, {"z", 5}}, []string{"x", "y", "z"}},
		{"negative and zero", []rr{{"zero", 0}, {"neg", -10}, {"pos", 1}}, []string{"neg", "zero", "pos"}},
		{"mixed", []rr{{"b1", 2}, {"a", 1}, {"b2", 2}, {"c", 3}}, []string{"a", "b1", "b2", "c"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newReg(t, rules.DefaultConfig(), "*all")
			for _, x := range tc.regs {
				mustRegister(t, r, "all", "", logRule(x.id, x.prio, rules.StopNone))
			}
			c := &tctx{}
			process(t, r, c)
			if !slices.Equal(c.log, tc.want) {
				t.Errorf("ran %v, want %v", c.log, tc.want)
			}
		})
	}
}

func TestLayerOrderBeatsPriority(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*first", "second", "third")
	// Registered in reverse layer order, with priorities that would reverse the result if layers were ignored.
	mustRegister(t, r, "third", "t", logRule("third", -100, rules.StopNone))
	mustRegister(t, r, "second", "s", logRule("second", 0, rules.StopNone))
	mustRegister(t, r, "first", "", logRule("first", 100, rules.StopNone))
	c := &tctx{scopes: map[string][]string{"second": {"s"}, "third": {"t"}}}
	res := process(t, r, c)
	if want := []string{"first", "second", "third"}; !slices.Equal(c.log, want) {
		t.Errorf("ran %v, want %v", c.log, want)
	}
	if res.Fired[0].Layer != "first" || res.Fired[2].Scope != "t" || res.Fired[2].RuleID != "third" {
		t.Errorf("Fired = %+v", res.Fired)
	}
}

func TestMultiKeyScopes(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		want []string
	}{
		{"priority interleaves keys, then key position", []string{"g2", "g1"}, []string{"g1-5", "g2-10", "g1-10", "g2-20"}},
		{"key order decides ties", []string{"g1", "g2"}, []string{"g1-5", "g1-10", "g2-10", "g2-20"}},
		{"duplicate keys count once", []string{"g1", "g1"}, []string{"g1-5", "g1-10"}},
		{"unknown keys are ignored", []string{"nope", "g1"}, []string{"g1-5", "g1-10"}},
		{"no keys, nothing fires", nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newReg(t, rules.DefaultConfig(), "group")
			for _, x := range []struct {
				scope string
				prio  int
			}{{"g1", 10}, {"g2", 20}, {"g2", 10}, {"g1", 5}} {
				mustRegister(t, r, "group", x.scope, logRule(x.scope+"-"+itoa(x.prio), x.prio, rules.StopNone))
			}
			c := &tctx{scopes: map[string][]string{"group": tc.keys}}
			process(t, r, c)
			if !slices.Equal(c.log, tc.want) {
				t.Errorf("ran %v, want %v", c.log, tc.want)
			}
		})
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestStopSemantics(t *testing.T) {
	type reg struct {
		layer string
		rule  rules.Rule[*tctx]
	}
	tests := []struct {
		name    string
		rules   []reg
		want    []string
		stopped rules.StopMode
	}{
		{"none runs everything",
			[]reg{{"a", logRule("a1", 1, rules.StopNone)}, {"a", logRule("a2", 2, rules.StopNone)}, {"b", logRule("b1", 1, rules.StopNone)}},
			[]string{"a1", "a2", "b1"}, rules.StopNone},
		{"layer skips the rest of this layer only",
			[]reg{{"a", logRule("a1", 1, rules.StopLayer)}, {"a", logRule("a2", 2, rules.StopNone)}, {"b", logRule("b1", 1, rules.StopNone)}},
			[]string{"a1", "b1"}, rules.StopLayer},
		{"all skips this layer's rest and every later layer",
			[]reg{{"a", logRule("a1", 1, rules.StopAll)}, {"a", logRule("a2", 2, rules.StopNone)}, {"b", logRule("b1", 1, rules.StopNone)}},
			[]string{"a1"}, rules.StopAll},
		{"stop in the middle keeps earlier rules",
			[]reg{{"a", logRule("a1", 1, rules.StopNone)}, {"a", logRule("a2", 2, rules.StopLayer)}, {"a", logRule("a3", 3, rules.StopNone)}, {"b", logRule("b1", 1, rules.StopNone)}},
			[]string{"a1", "a2", "b1"}, rules.StopLayer},
		{"a stopped layer does not stop a later StopAll from being reported",
			[]reg{{"a", logRule("a1", 1, rules.StopLayer)}, {"b", logRule("b1", 1, rules.StopAll)}, {"c", logRule("c1", 1, rules.StopNone)}},
			[]string{"a1", "b1"}, rules.StopAll},
		{"stop-only rule with no action",
			[]reg{{"a", rules.Rule[*tctx]{ID: "gate", Event: "ev", Priority: 1, Stop: rules.StopLayer}}, {"a", logRule("a2", 2, rules.StopNone)}},
			nil, rules.StopLayer},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newReg(t, rules.DefaultConfig(), "*a", "*b", "*c")
			for _, x := range tc.rules {
				mustRegister(t, r, x.layer, "", x.rule)
			}
			c := &tctx{}
			res := process(t, r, c)
			if !slices.Equal(c.log, tc.want) {
				t.Errorf("ran %v, want %v", c.log, tc.want)
			}
			if res.Stopped != tc.stopped {
				t.Errorf("Stopped = %q, want %q", res.Stopped, tc.stopped)
			}
		})
	}
}

func TestFalseConditionNeverStops(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*a")
	gate := logRule("gate", 1, rules.StopAll)
	gate.When = func(*tctx) bool { return false }
	mustRegister(t, r, "a", "", gate)
	mustRegister(t, r, "a", "", logRule("after", 2, rules.StopNone))
	c := &tctx{}
	res := process(t, r, c)
	if !slices.Equal(c.log, []string{"after"}) || res.Stopped != rules.StopNone {
		t.Errorf("ran %v stopped %q; a false condition must neither fire nor stop", c.log, res.Stopped)
	}
}

func TestConditionGatesAction(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*a")
	rule := logRule("only-if-flagged", 0, rules.StopNone)
	rule.When = func(c *tctx) bool { return len(c.log) == 0 }
	mustRegister(t, r, "a", "", rule)
	for i, wantFired := range []int{1, 0} {
		c := &tctx{}
		if i == 1 {
			c.log = []string{"already"}
		}
		if got := len(process(t, r, c).Fired); got != wantFired {
			t.Errorf("case %d: %d fired, want %d", i, got, wantFired)
		}
	}
}

func TestEventTypeMatchesExactly(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*a")
	mustRegister(t, r, "a", "", logRule("r", 0, rules.StopNone))
	for ev, want := range map[string]int{"ev": 1, "EV": 0, "": 0, "other": 0} {
		res, err := r.Process(&tctx{}, ev)
		if err != nil || len(res.Fired) != want {
			t.Errorf("event %q: %d fired err %v, want %d", ev, len(res.Fired), err, want)
		}
	}
}

func TestOnActionError(t *testing.T) {
	boom := errors.New("boom")
	build := func(cfg rules.Config) (*reg, *tctx) {
		r := newReg(t, cfg, "*a", "*b")
		failing := logRule("fails", 1, rules.StopLayer)
		failing.Do = func(c *tctx) error { c.log = append(c.log, "fails"); return boom }
		mustRegister(t, r, "a", "", failing)
		mustRegister(t, r, "a", "", logRule("a2", 2, rules.StopNone))
		mustRegister(t, r, "b", "", logRule("b1", 1, rules.StopNone))
		return r, &tctx{}
	}
	t.Run("default continues, and a failed rule still applies its stop", func(t *testing.T) {
		if got := rules.DefaultConfig().OnActionError; got != "continue" {
			t.Fatalf("default OnActionError = %q", got)
		}
		r, c := build(rules.DefaultConfig())
		res, err := r.Process(c, "ev")
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "a//fails") {
			t.Errorf("err = %v, want it to wrap boom and name the rule", err)
		}
		if want := []string{"fails", "b1"}; !slices.Equal(c.log, want) || len(res.Fired) != 2 {
			t.Errorf("ran %v, want %v", c.log, want)
		}
	})
	t.Run("stop returns right after the failing rule", func(t *testing.T) {
		r, c := build(rules.Config{OnActionError: "stop"})
		res, err := r.Process(c, "ev")
		if !errors.Is(err, boom) {
			t.Errorf("err = %v", err)
		}
		if want := []string{"fails"}; !slices.Equal(c.log, want) || res.Stopped != rules.StopNone || len(res.Fired) != 1 {
			t.Errorf("ran %v stopped %q, want %v and no stop", c.log, res.Stopped, want)
		}
	})
	t.Run("several errors are joined", func(t *testing.T) {
		r := newReg(t, rules.DefaultConfig(), "*a")
		e1, e2 := errors.New("e1"), errors.New("e2")
		for i, e := range []error{e1, e2} {
			rule := logRule("r"+itoa(i), i, rules.StopNone)
			rule.Do = func(*tctx) error { return e }
			mustRegister(t, r, "a", "", rule)
		}
		_, err := r.Process(&tctx{}, "ev")
		if !errors.Is(err, e1) || !errors.Is(err, e2) {
			t.Errorf("err = %v, want both errors", err)
		}
	})
}

// The zero Config is useful: an empty OnActionError behaves as "continue".
func TestZeroConfigWorks(t *testing.T) {
	boom := errors.New("boom")
	r, err := rules.NewRegistry(rules.Config{}, rules.Layer[*tctx]{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	failing := logRule("fails", 1, rules.StopNone)
	failing.Do = func(*tctx) error { return boom }
	mustRegister(t, r, "a", "", failing)
	mustRegister(t, r, "a", "", logRule("after", 2, rules.StopNone))
	c := &tctx{}
	res, err := r.Process(c, "ev")
	if !errors.Is(err, boom) || len(res.Fired) != 2 {
		t.Errorf("zero Config should continue past errors: fired %d, err %v", len(res.Fired), err)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name string
		cfg  rules.Config
		bad  bool
	}{
		{"default", rules.DefaultConfig(), false},
		{"stop", rules.Config{OnActionError: "stop"}, false},
		{"empty means continue", rules.Config{}, false},
		{"unknown", rules.Config{OnActionError: "ignore"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.bad || (tc.bad && !errors.Is(err, rules.ErrInvalidConfig)) {
				t.Errorf("Validate() = %v, want error: %v", err, tc.bad)
			}
			if _, nerr := rules.NewRegistry[*tctx](tc.cfg); (nerr != nil) != tc.bad {
				t.Errorf("NewRegistry err = %v, want error: %v", nerr, tc.bad)
			}
		})
	}
}

func TestNewRegistryLayerErrors(t *testing.T) {
	tests := []struct {
		name   string
		layers []rules.Layer[*tctx]
		bad    bool
	}{
		{"none is allowed", nil, false},
		{"empty name", []rules.Layer[*tctx]{{Name: ""}}, true},
		{"duplicate name", []rules.Layer[*tctx]{{Name: "a"}, {Name: "a"}}, true},
		{"ok", []rules.Layer[*tctx]{{Name: "a"}, {Name: "b"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := rules.NewRegistry(rules.DefaultConfig(), tc.layers...)
			if (err != nil) != tc.bad || (tc.bad && !errors.Is(err, rules.ErrInvalidLayer)) {
				t.Errorf("err = %v, want ErrInvalidLayer: %v", err, tc.bad)
			}
		})
	}
}

func TestRegisterErrors(t *testing.T) {
	tests := []struct {
		name         string
		layer, scope string
		rule         rules.Rule[*tctx]
		want         error
	}{
		{"ok", "u", "", logRule("r", 0, rules.StopNone), nil},
		{"ok keyed", "k", "anything", logRule("r", 0, rules.StopNone), nil},
		{"unknown layer", "nope", "", logRule("r", 0, rules.StopNone), rules.ErrUnknownLayer},
		{"empty id", "u", "", rules.Rule[*tctx]{Event: "ev"}, rules.ErrInvalidRule},
		{"empty event", "u", "", rules.Rule[*tctx]{ID: "r"}, rules.ErrInvalidRule},
		{"bad stop", "u", "", logRule("r", 0, "sometimes"), rules.ErrInvalidRule},
		{"scope on universal layer", "u", "x", logRule("r", 0, rules.StopNone), rules.ErrInvalidRule},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newReg(t, rules.DefaultConfig(), "*u", "k")
			err := r.Register(tc.layer, tc.scope, tc.rule)
			if !errors.Is(err, tc.want) && !(err == nil && tc.want == nil) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDuplicateAndUnregister(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "k")
	mustRegister(t, r, "k", "s1", logRule("same", 0, rules.StopNone))
	if err := r.Register("k", "s1", logRule("same", 5, rules.StopNone)); !errors.Is(err, rules.ErrDuplicateRule) {
		t.Errorf("same layer/scope/ID: err = %v, want ErrDuplicateRule", err)
	}
	dup := logRule("same", 0, rules.StopNone)
	dup.Event = "other-event"
	if err := r.Register("k", "s1", dup); !errors.Is(err, rules.ErrDuplicateRule) {
		t.Errorf("IDs are unique across event types too: err = %v", err)
	}
	mustRegister(t, r, "k", "s2", logRule("same", 0, rules.StopNone)) // other scope: fine

	c := &tctx{scopes: map[string][]string{"k": {"s1", "s2"}}}
	if n := len(process(t, r, c).Fired); n != 2 {
		t.Fatalf("%d fired, want 2", n)
	}
	if !r.Unregister("k", "s1", "same") || r.Unregister("k", "s1", "same") || r.Unregister("k", "s1", "missing") {
		t.Error("Unregister should report true once, then false")
	}
	if n := len(process(t, r, &tctx{scopes: c.scopes}).Fired); n != 1 {
		t.Errorf("%d fired after unregister, want 1", n)
	}
	mustRegister(t, r, "k", "s1", logRule("same", 0, rules.StopNone)) // ID is free again
}

func TestScopesAreEvaluatedPerLayer(t *testing.T) {
	// An action in the first layer changes the context; the second layer's
	// Scopes sees the change.
	type c struct{ group string }
	r, _ := rules.NewRegistry(rules.DefaultConfig(),
		rules.Layer[*c]{Name: "first"},
		rules.Layer[*c]{Name: "second", Scopes: func(x *c) []string { return []string{x.group} }},
	)
	var ran []string
	r.Register("first", "", rules.Rule[*c]{ID: "promote", Event: "ev", Do: func(x *c) error { x.group = "vip"; return nil }})
	r.Register("second", "vip", rules.Rule[*c]{ID: "vip-rule", Event: "ev", Do: func(*c) error { ran = append(ran, "vip"); return nil }})
	r.Register("second", "plain", rules.Rule[*c]{ID: "plain-rule", Event: "ev", Do: func(*c) error { ran = append(ran, "plain"); return nil }})
	if _, err := r.Process(&c{group: "plain"}, "ev"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ran, []string{"vip"}) {
		t.Errorf("ran %v, want [vip]", ran)
	}
}

func TestRegisterDuringProcessDoesNotDeadlock(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*a")
	rule := logRule("spawner", 0, rules.StopNone)
	rule.Do = func(c *tctx) error {
		c.log = append(c.log, "spawner")
		return r.Register("a", "", logRule("spawned", 1, rules.StopNone))
	}
	mustRegister(t, r, "a", "", rule)
	c := &tctx{}
	process(t, r, c)
	if !slices.Equal(c.log, []string{"spawner"}) {
		t.Errorf("first run %v: a rule registered mid-Process must wait for the next one", c.log)
	}
	// Second Process: the spawner now fails to re-register (duplicate); the new rule runs.
	c = &tctx{}
	_, err := r.Process(c, "ev")
	if !errors.Is(err, rules.ErrDuplicateRule) || !slices.Equal(c.log, []string{"spawner", "spawned"}) {
		t.Errorf("second run %v err %v", c.log, err)
	}
}

func TestDeterministic(t *testing.T) {
	build := func() *reg {
		r := newReg(t, rules.DefaultConfig(), "*all", "group")
		for i, scope := range []string{"b", "a", "b", "a"} {
			mustRegister(t, r, "group", scope, logRule(scope+itoa(i), i%2, rules.StopNone))
		}
		mustRegister(t, r, "all", "", logRule("u", 0, rules.StopNone))
		return r
	}
	var first []string
	for run := range 50 {
		c := &tctx{scopes: map[string][]string{"group": {"b", "a"}}}
		process(t, build(), c)
		if run == 0 {
			first = c.log
			continue
		}
		if !slices.Equal(first, c.log) {
			t.Fatalf("run %d gave %v, first gave %v", run, c.log, first)
		}
	}
}

func TestConcurrentUse(t *testing.T) {
	r := newReg(t, rules.DefaultConfig(), "*a")
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 100 {
				id := "r" + itoa(g) + "-" + itoa(i%100)
				_ = r.Register("a", "", logRule(id, i, rules.StopNone))
				if _, err := r.Process(&tctx{}, "ev"); err != nil {
					t.Error(err)
				}
				r.Unregister("a", "", id)
			}
		}()
	}
	wg.Wait()
}

// The package works with any context type, including a plain value.
func TestContextCanBeAValue(t *testing.T) {
	r, _ := rules.NewRegistry(rules.DefaultConfig(), rules.Layer[int]{Name: "n"})
	hit := false
	r.Register("n", "", rules.Rule[int]{ID: "r", Event: "ev", When: func(n int) bool { return n > 3 },
		Do: logic.Action[int](func(int) error { hit = true; return nil })})
	r.Process(2, "ev")
	if hit {
		t.Fatal("fired for 2")
	}
	r.Process(5, "ev")
	if !hit {
		t.Fatal("did not fire for 5")
	}
}
