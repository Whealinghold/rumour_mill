package declare_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Whealinghold/rumour_mill/declare"
	"github.com/Whealinghold/rumour_mill/rules"
)

func cxRegistry(t *testing.T) *rules.Registry[cx] {
	t.Helper()
	reg, err := rules.NewRegistry(rules.DefaultConfig(), rules.Layer[cx]{Name: "u"}, rules.Layer[cx]{Name: "k", Scopes: func(cx) []string { return []string{"s"} }})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func say(m string) declare.Node { return node("say", declare.Args{"msg": m}) }

func TestDefaultRulesConfig(t *testing.T) {
	c := declare.DefaultRulesConfig()
	if c.DefaultPriority != 100 || c.OnActionError != "continue" || c.Validate() != nil {
		t.Errorf("DefaultRulesConfig() = %+v", c)
	}
}

func TestLoadRulesPriorityDefault(t *testing.T) {
	zero, one := 0, 1
	tests := []struct {
		name string
		def  *int // nil = default 100
		a, b *int // priorities of the two rules; nil = omitted
		want []string
	}{
		// "late" is registered first; with equal priorities it would run first.
		{"omitted gets the default of 100, so an explicit 99 beats it", nil, nil, ptr(99), []string{"b", "a"}},
		{"override default to 0: omitted runs before explicit 1", &zero, nil, &one, []string{"a", "b"}},
		{"override default to 200: explicit 100 runs before omitted", ptr(200), nil, ptr(100), []string{"b", "a"}},
		{"an explicit 0 is respected, not treated as omitted", nil, &zero, ptr(50), []string{"a", "b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := declare.DefaultRulesConfig()
			if tc.def != nil {
				cfg.DefaultPriority = *tc.def
			}
			reg := cxRegistry(t)
			specs := []declare.RuleSpec{
				{ID: "a", Layer: "u", Event: "ev", Priority: tc.a, Do: []declare.Node{say("a")}},
				{ID: "b", Layer: "u", Event: "ev", Priority: tc.b, Do: []declare.Node{say("b")}},
			}
			if err := declare.LoadRules(reg, cfg, specs, testCatalog(t)); err != nil {
				t.Fatal(err)
			}
			c := newCx(0)
			if _, err := reg.Process(c, "ev"); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(*c.log, tc.want) {
				t.Errorf("ran %v, want %v", *c.log, tc.want)
			}
		})
	}
}

func TestLoadRulesBehaviour(t *testing.T) {
	reg := cxRegistry(t)
	specs := []declare.RuleSpec{
		{ID: "gate", Layer: "u", Event: "ev", Priority: ptr(1), Stop: rules.StopLayer, When: &declare.Node{Use: "gt", Args: declare.Args{"value": 5}}, Do: []declare.Node{say("gate")}},
		{ID: "later", Layer: "u", Event: "ev", Priority: ptr(2), Do: []declare.Node{say("later")}},
		{ID: "scoped", Layer: "k", Scope: "s", Event: "ev", Do: []declare.Node{say("scoped")}},
		{ID: "no-action", Layer: "k", Scope: "s", Event: "other"},
	}
	if err := declare.LoadRules(reg, declare.DefaultRulesConfig(), specs, testCatalog(t)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		n    int
		want []string
	}{{9, []string{"gate", "scoped"}}, {1, []string{"later", "scoped"}}} {
		c := newCx(tc.n)
		reg.Process(c, "ev")
		if !slices.Equal(*c.log, tc.want) {
			t.Errorf("n=%d ran %v, want %v (condition gates the rule; stop applies only when it fires)", tc.n, *c.log, tc.want)
		}
	}
	if res, err := reg.Process(newCx(0), "other"); err != nil || len(res.Fired) != 1 {
		t.Errorf("a rule with no do and no when fires and does nothing: %+v %v", res, err)
	}
}

func TestLoadRulesErrors(t *testing.T) {
	tests := []struct {
		name  string
		specs []declare.RuleSpec
		want  error
		path  string
	}{
		{"unknown name in when", []declare.RuleSpec{{ID: "a", Layer: "u", Event: "ev", When: &declare.Node{Use: "evn"}}}, declare.ErrUnknownName, "rules[0].when:"},
		{"nested path in when", []declare.RuleSpec{{ID: "a", Layer: "u", Event: "ev"},
			{ID: "b", Layer: "u", Event: "ev", When: &declare.Node{Use: "all", Of: []declare.Node{{Use: "even"}, {Use: "evn"}}}}}, declare.ErrUnknownName, "rules[1].when.of[1]:"},
		{"unknown name in do", []declare.RuleSpec{{ID: "a", Layer: "u", Event: "ev", Do: []declare.Node{say("x"), {Use: "sya"}}}}, declare.ErrUnknownName, "rules[0].do[1]:"},
		{"bad args in do", []declare.RuleSpec{{ID: "a", Layer: "u", Event: "ev", Do: []declare.Node{{Use: "say"}}}}, declare.ErrBadArgs, "rules[0].do[0]"},
		{"condition used in do", []declare.RuleSpec{{ID: "a", Layer: "u", Event: "ev", Do: []declare.Node{{Use: "even"}}}}, declare.ErrWrongKind, "rules[0].do[0]:"},
		{"action used in when", []declare.RuleSpec{{ID: "a", Layer: "u", Event: "ev", When: &declare.Node{Use: "fail"}}}, declare.ErrWrongKind, "rules[0].when:"},
		{"unknown layer", []declare.RuleSpec{{ID: "a", Layer: "nope", Event: "ev"}}, rules.ErrUnknownLayer, "rules[0] (a)"},
		{"missing id", []declare.RuleSpec{{Layer: "u", Event: "ev"}}, rules.ErrInvalidRule, "rules[0]"},
		{"missing event", []declare.RuleSpec{{ID: "a", Layer: "u"}}, rules.ErrInvalidRule, "rules[0] (a)"},
		{"bad stop mode", []declare.RuleSpec{{ID: "a", Layer: "u", Event: "ev", Stop: "sometimes"}}, rules.ErrInvalidRule, "rules[0] (a)"},
		{"scope on a universal layer", []declare.RuleSpec{{ID: "a", Layer: "u", Scope: "x", Event: "ev"}}, rules.ErrInvalidRule, "rules[0] (a)"},
		{"duplicate within the batch", []declare.RuleSpec{{ID: "a", Layer: "u", Event: "ev"}, {ID: "a", Layer: "u", Event: "ev"}}, rules.ErrDuplicateRule, "rules[1] (a)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := cxRegistry(t)
			err := declare.LoadRules(reg, declare.DefaultRulesConfig(), tc.specs, testCatalog(t))
			if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.path) {
				t.Errorf("err = %v, want %v mentioning %q", err, tc.want, tc.path)
			}
			// All-or-nothing: whatever the failure, no rule of the batch remains.
			c := newCx(0)
			if res, _ := reg.Process(c, "ev"); len(res.Fired) != 0 {
				t.Errorf("a failed load left rules behind: %+v", res.Fired)
			}
		})
	}
}

func TestLoadRulesReportsEverySpecWithAProblem(t *testing.T) {
	specs := []declare.RuleSpec{
		{ID: "a", Layer: "u", Event: "ev", When: &declare.Node{Use: "x"}},
		{ID: "b", Layer: "u", Event: "ev"},
		{ID: "c", Layer: "u", Event: "ev", Do: []declare.Node{{Use: "y"}}},
	}
	err := declare.LoadRules(cxRegistry(t), declare.DefaultRulesConfig(), specs, testCatalog(t))
	if err == nil || !strings.Contains(err.Error(), "rules[0].when") || !strings.Contains(err.Error(), "rules[2].do[0]") {
		t.Errorf("err = %v, want both problems", err)
	}
}

func TestLoadRulesDoesNotTouchEarlierRules(t *testing.T) {
	reg := cxRegistry(t)
	cat := testCatalog(t)
	if err := declare.LoadRules(reg, declare.DefaultRulesConfig(), []declare.RuleSpec{{ID: "keep", Layer: "u", Event: "ev", Do: []declare.Node{say("keep")}}}, cat); err != nil {
		t.Fatal(err)
	}
	// The second load collides with "keep" after registering "new"; both of its rules must go, "keep" must stay.
	err := declare.LoadRules(reg, declare.DefaultRulesConfig(), []declare.RuleSpec{
		{ID: "new", Layer: "u", Event: "ev", Do: []declare.Node{say("new")}},
		{ID: "keep", Layer: "u", Event: "ev"},
	}, cat)
	if !errors.Is(err, rules.ErrDuplicateRule) {
		t.Fatalf("err = %v", err)
	}
	c := newCx(0)
	reg.Process(c, "ev")
	if !slices.Equal(*c.log, []string{"keep"}) {
		t.Errorf("ran %v, want only the earlier rule", *c.log)
	}
}
