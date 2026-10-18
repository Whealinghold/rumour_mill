package infer_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
)

func store(t *testing.T) *knowledge.Store {
	t.Helper()
	s, err := knowledge.NewStore(knowledge.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func put(t *testing.T, s *knowledge.Store, typ, subject string, c float64) {
	t.Helper()
	if _, err := s.Add(knowledge.Fact{Type: typ, Subject: subject, Confidence: c, Source: "observed"}); err != nil {
		t.Fatal(err)
	}
}

func engine(t *testing.T, cfg infer.Config, rules ...infer.Rule) *infer.Engine {
	t.Helper()
	e, err := infer.New(cfg, rules)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func run(t *testing.T, e *infer.Engine, s *knowledge.Store, now knowledge.Tick) infer.Result {
	t.Helper()
	res, err := e.Run(s, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// ids lists the derived fact IDs of a result.
func ids(res infer.Result) []string {
	var out []string
	for _, d := range res.Derived {
		out = append(out, d.ID)
	}
	return out
}

func simple(id, from, to string, c float64) infer.Rule {
	return infer.Rule{ID: id, Requires: []infer.Requirement{{Type: from}}, Produces: infer.Product{Type: to}, Confidence: c}
}

func TestDefaultConfigValues(t *testing.T) {
	want := infer.Config{MaxPasses: 1, IDPrefix: "inferred", DerivedKind: "derived"}
	if g := infer.DefaultConfig(); g != want {
		t.Errorf("DefaultConfig() = %+v, want %+v", g, want)
	}
	if err := infer.DefaultConfig().Validate(); err != nil {
		t.Error(err)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*infer.Config)
		bad    bool
	}{
		{"default", func(*infer.Config) {}, false},
		{"zero passes", func(c *infer.Config) { c.MaxPasses = 0 }, true},
		{"negative passes", func(c *infer.Config) { c.MaxPasses = -2 }, true},
		{"empty prefix", func(c *infer.Config) { c.IDPrefix = "" }, true},
		{"empty kind", func(c *infer.Config) { c.DerivedKind = "" }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := infer.DefaultConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if (err != nil) != tc.bad || (tc.bad && !errors.Is(err, infer.ErrInvalidConfig)) {
				t.Errorf("Validate() = %v, want error: %v", err, tc.bad)
			}
			if _, nerr := infer.New(cfg, nil); (nerr != nil) != tc.bad {
				t.Errorf("New err = %v", nerr)
			}
		})
	}
	if err := (infer.Config{}).Validate(); err == nil {
		t.Error("the zero Config must be rejected; start from DefaultConfig")
	}
}

// Each test below asserts a default and shows that overriding it changes behaviour.

func TestMaxPasses(t *testing.T) {
	rules := []infer.Rule{simple("r1", "a", "b", 0.5), simple("r2", "b", "c", 0.5), simple("r3", "c", "d", 0.5)}
	tests := []struct {
		name       string
		passes     int // 0 = default
		wantTypes  []string
		wantPasses int
	}{
		{"default is a single pass", 0, []string{"b"}, 1},
		{"two passes", 2, []string{"b", "c"}, 2},
		{"three passes", 3, []string{"b", "c", "d"}, 3},
		{"stops early when nothing changes", 10, []string{"b", "c", "d"}, 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := infer.DefaultConfig()
			if tc.passes != 0 {
				cfg.MaxPasses = tc.passes
			}
			s := store(t)
			put(t, s, "a", "x", 0.9)
			res := run(t, engine(t, cfg, rules...), s, 0)
			var types []string
			for _, d := range res.Derived {
				f, _ := s.Get(d.ID)
				types = append(types, f.Type)
			}
			if !slices.Equal(types, tc.wantTypes) || res.Passes != tc.wantPasses {
				t.Errorf("derived %v in %d passes, want %v in %d", types, res.Passes, tc.wantTypes, tc.wantPasses)
			}
		})
	}
}

func TestIDPrefixAndDerivedKind(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, kind, wantID, wantKind string
	}{
		{"defaults", "", "", "inferred:r:x", "derived"},
		{"overrides", "deduction", "guess", "deduction:r:x", "guess"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := infer.DefaultConfig()
			if tc.prefix != "" {
				cfg.IDPrefix, cfg.DerivedKind = tc.prefix, tc.kind
			}
			s := store(t)
			put(t, s, "a", "x", 0.9)
			res := run(t, engine(t, cfg, simple("r", "a", "b", 0.5)), s, 0)
			f, ok := s.Get(tc.wantID)
			if !ok || f.Source != tc.wantKind || !slices.Equal(ids(res), []string{tc.wantID}) {
				t.Errorf("derived %v, fact %+v, want ID %q kind %q", ids(res), f, tc.wantID, tc.wantKind)
			}
		})
	}
}

func TestDerivedFactFields(t *testing.T) {
	s := store(t)
	s.Add(knowledge.Fact{ID: "p1", Type: "a", Subject: "x", Confidence: 0.9, Source: "observed"})
	s.Add(knowledge.Fact{ID: "p2", Type: "b", Subject: "x", Confidence: 0.6, Source: "observed"})
	e := engine(t, infer.DefaultConfig(), infer.Rule{
		ID:         "r",
		Requires:   []infer.Requirement{{Type: "a"}, {Type: "b"}},
		Produces:   infer.Product{Type: "c", TTL: 100, Private: true, Payload: "why"},
		Confidence: 0.55,
	})
	run(t, e, s, 40)
	got, ok := s.Get("inferred:r:x")
	exp := knowledge.Tick(140)
	want := knowledge.Fact{ID: "inferred:r:x", Type: "c", Subject: "x", Payload: "why", Confidence: 0.55, Source: "derived",
		Prov: knowledge.Provenance{Premises: []string{"p1", "p2"}}, Observed: 40, Expiry: &exp, Private: true}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestNoTTLMeansNoExpiry(t *testing.T) {
	s := store(t)
	put(t, s, "a", "x", 0.9)
	run(t, engine(t, infer.DefaultConfig(), simple("r", "a", "b", 0.5)), s, 7)
	if f, _ := s.Get("inferred:r:x"); f.Expiry != nil {
		t.Errorf("expiry = %v, want none", *f.Expiry)
	}
}

func TestSubjectBinding(t *testing.T) {
	two := func(prod infer.Product) infer.Rule {
		return infer.Rule{ID: "r", Requires: []infer.Requirement{{Type: "a"}, {Type: "b"}}, Produces: prod, Confidence: 0.5}
	}
	tests := []struct {
		name  string
		facts [][2]string // type, subject
		rule  infer.Rule
		want  []string // derived subjects, ascending
	}{
		{"fires per subject satisfying every requirement",
			[][2]string{{"a", "y"}, {"b", "y"}, {"a", "x"}, {"b", "x"}, {"a", "z"}, {"b", "w"}},
			two(infer.Product{Type: "c"}), []string{"x", "y"}},
		{"unbound requirements must agree on the subject",
			[][2]string{{"a", "x"}, {"b", "y"}},
			two(infer.Product{Type: "c"}), nil},
		{"fixed product subject",
			[][2]string{{"a", "x"}, {"b", "x"}, {"a", "y"}, {"b", "y"}},
			two(infer.Product{Type: "c", Subject: "target"}), []string{"target"}},
		{"fixed requirement is checked independently",
			[][2]string{{"a", "x"}, {"flag", "global"}},
			infer.Rule{ID: "r", Requires: []infer.Requirement{{Type: "a"}, {Type: "flag", Subject: "global"}}, Produces: infer.Product{Type: "c"}, Confidence: 0.5},
			[]string{"x"}},
		{"missing fixed requirement blocks the rule",
			[][2]string{{"a", "x"}, {"flag", "other"}},
			infer.Rule{ID: "r", Requires: []infer.Requirement{{Type: "a"}, {Type: "flag", Subject: "global"}}, Produces: infer.Product{Type: "c"}, Confidence: 0.5},
			nil},
		{"all fixed: fires once for the product subject",
			[][2]string{{"a", "p"}, {"b", "q"}},
			infer.Rule{ID: "r", Requires: []infer.Requirement{{Type: "a", Subject: "p"}, {Type: "b", Subject: "q"}}, Produces: infer.Product{Type: "c", Subject: "out"}, Confidence: 0.5},
			[]string{"out"}},
		{"same type twice binds the same subject",
			[][2]string{{"a", "x"}},
			infer.Rule{ID: "r", Requires: []infer.Requirement{{Type: "a"}, {Type: "a", MinConfidence: ptr(0.5)}}, Produces: infer.Product{Type: "c"}, Confidence: 0.5},
			[]string{"x"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := store(t)
			for _, f := range tc.facts {
				put(t, s, f[0], f[1], 0.9)
			}
			res := run(t, engine(t, infer.DefaultConfig(), tc.rule), s, 0)
			var subjects []string
			for _, d := range res.Derived {
				f, _ := s.Get(d.ID)
				subjects = append(subjects, f.Subject)
			}
			if !slices.Equal(subjects, tc.want) {
				t.Errorf("derived subjects %v, want %v", subjects, tc.want)
			}
		})
	}
}

func TestRequirementFilters(t *testing.T) {
	tests := []struct {
		name string
		req  infer.Requirement
		now  knowledge.Tick
		want bool
	}{
		{"default floor is the store's query floor (0.2): 0.25 passes", infer.Requirement{Type: "a"}, 0, true},
		{"explicit floor above the fact", infer.Requirement{Type: "a", MinConfidence: ptr(0.3)}, 0, false},
		{"explicit floor of 0 sees facts hidden from default queries", infer.Requirement{Type: "a", Subject: "weak", MinConfidence: ptr(0.0)}, 0, true},
		{"source in list", infer.Requirement{Type: "a", Sources: []string{"tip", "observed"}}, 0, true},
		{"source not in list", infer.Requirement{Type: "a", Sources: []string{"tip"}}, 0, false},
		{"expired premises do not count", infer.Requirement{Type: "a"}, 100, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := store(t)
			exp := knowledge.Tick(100)
			s.Add(knowledge.Fact{Type: "a", Subject: "x", Confidence: 0.25, Source: "observed", Expiry: &exp})
			s.Add(knowledge.Fact{Type: "a", Subject: "weak", Confidence: 0.05, Source: "observed"})
			rule := infer.Rule{ID: "r", Requires: []infer.Requirement{tc.req}, Produces: infer.Product{Type: "b"}, Confidence: 0.5}
			if tc.req.Subject != "" {
				rule.Produces.Subject = tc.req.Subject
			}
			res := run(t, engine(t, infer.DefaultConfig(), rule), s, tc.now)
			if (len(res.Derived) > 0) != tc.want {
				t.Errorf("derived %v, want fired: %v", ids(res), tc.want)
			}
		})
	}
}

func TestBestPremiseAndDedupe(t *testing.T) {
	s := store(t)
	for _, f := range []knowledge.Fact{
		{ID: "weak", Type: "a", Subject: "x", Confidence: 0.4, Source: "observed", Discriminator: "1"},
		{ID: "strong", Type: "a", Subject: "x", Confidence: 0.8, Source: "observed", Discriminator: "2"},
		{ID: "tie-b", Type: "a", Subject: "x", Confidence: 0.8, Source: "observed", Discriminator: "3"},
	} {
		s.Add(f)
	}
	// Two requirements on the same type pick the same best fact: listed once.
	e := engine(t, infer.DefaultConfig(), infer.Rule{ID: "r", Requires: []infer.Requirement{{Type: "a"}, {Type: "a"}}, Produces: infer.Product{Type: "b"}, Confidence: 0.5})
	run(t, e, s, 0)
	if f, _ := s.Get("inferred:r:x"); !slices.Equal(f.Prov.Premises, []string{"strong"}) {
		t.Errorf("premises %v, want [strong] (highest confidence, ties by lowest ID, deduplicated)", f.Prov.Premises)
	}
}

func TestIdempotentAndMerge(t *testing.T) {
	s := store(t)
	put(t, s, "a", "x", 0.9)
	e := engine(t, infer.DefaultConfig(), simple("r", "a", "b", 0.5))
	if first := run(t, e, s, 0); len(first.Derived) != 1 {
		t.Fatalf("first run derived %v", ids(first))
	}
	if second := run(t, e, s, 1); len(second.Derived) != 0 || second.Passes != 1 {
		t.Errorf("second run %+v; re-deriving an unchanged conclusion must be a no-op", second)
	}
	// A stronger rule with the same ID cannot exist (IDs are unique), but a
	// stronger derivation of the same conclusion replaces a weaker one.
	e2 := engine(t, infer.DefaultConfig(), infer.Rule{ID: "r", Requires: []infer.Requirement{{Type: "a"}}, Produces: infer.Product{Type: "b"}, Confidence: 0.9})
	if res := run(t, e2, s, 2); len(res.Derived) != 1 || res.Derived[0].Outcome != knowledge.Replaced {
		t.Errorf("stronger derivation should replace: %+v", res.Derived)
	}
}

func TestDerivedDoesNotOverrideBetterKnowledge(t *testing.T) {
	s := store(t)
	put(t, s, "a", "x", 0.9)
	s.Add(knowledge.Fact{ID: "inferred:r:x", Type: "b", Subject: "x", Confidence: 0.3, Source: "observed"}) // seen first-hand
	res := run(t, engine(t, infer.DefaultConfig(), simple("r", "a", "b", 0.9)), s, 0)
	if f, _ := s.Get("inferred:r:x"); len(res.Derived) != 0 || f.Source != "observed" {
		t.Errorf("a derived fact overwrote higher-ranked knowledge: %+v", f)
	}
}

// Termination: facts produced by inference may feed rules, even in a cycle, and
// Run still stops. Derived IDs are limited to rules x subjects and an ID can only
// be replaced by a higher confidence, so the store reaches a fixed point; here we
// give the engine far more passes than it needs and check it stops on its own.
func TestTerminationOnCycles(t *testing.T) {
	cfg := infer.DefaultConfig()
	cfg.MaxPasses = 1_000_000
	tests := []struct {
		name  string
		rules []infer.Rule
	}{
		{"self feeding", []infer.Rule{simple("loop", "a", "a", 0.9)}},
		{"two rule cycle", []infer.Rule{simple("ab", "a", "b", 0.9), simple("ba", "b", "a", 0.9)}},
		{"cycle with rising confidence", []infer.Rule{simple("ab", "a", "b", 0.5), simple("ba", "b", "a", 0.6), simple("aa", "a", "a", 0.95)}},
		{"cycle that creates new subjects only from existing ones", []infer.Rule{
			{ID: "spread", Requires: []infer.Requirement{{Type: "a"}}, Produces: infer.Product{Type: "a", Subject: "other"}, Confidence: 0.9},
			simple("back", "a", "a", 0.9)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := store(t)
			put(t, s, "a", "x", 0.4)
			e := engine(t, cfg, tc.rules...)
			res := run(t, e, s, 0)
			if res.Passes > 10 {
				t.Errorf("took %d passes", res.Passes)
			}
			before := len(s.All())
			if again := run(t, e, s, 0); len(again.Derived) != 0 || len(s.All()) != before {
				t.Errorf("not at a fixed point: %+v", again)
			}
		})
	}
}

func TestRuleOrderDoesNotMatterWithinAPass(t *testing.T) {
	rules := []infer.Rule{simple("r1", "a", "b", 0.5), simple("r2", "b", "c", 0.5), simple("r3", "a", "d", 0.5)}
	final := func(rs []infer.Rule) []knowledge.Fact {
		s := store(t)
		put(t, s, "a", "x", 0.9)
		run(t, engine(t, infer.DefaultConfig(), rs...), s, 0)
		return s.All()
	}
	forward := final(rules)
	reversed := final([]infer.Rule{rules[2], rules[1], rules[0]})
	if !reflect.DeepEqual(forward, reversed) {
		t.Errorf("rule order changed the result:\n%v\n%v", forward, reversed)
	}
	if len(forward) != 3 { // a, plus b and d; c needs a second pass
		t.Errorf("a pass must only see the facts present when it began: %d facts", len(forward))
	}
}

func TestDeterministic(t *testing.T) {
	build := func(order []string) ([]string, []knowledge.Fact) {
		s := store(t)
		for _, subj := range order {
			put(t, s, "a", subj, 0.9)
			put(t, s, "b", subj, 0.9)
		}
		cfg := infer.DefaultConfig()
		cfg.MaxPasses = 3
		res := run(t, engine(t, cfg,
			infer.Rule{ID: "r1", Requires: []infer.Requirement{{Type: "a"}, {Type: "b"}}, Produces: infer.Product{Type: "c"}, Confidence: 0.5},
			simple("r2", "c", "d", 0.5)), s, 0)
		return ids(res), s.All()
	}
	firstIDs, firstAll := build([]string{"c", "a", "b"})
	for _, order := range [][]string{{"a", "b", "c"}, {"b", "c", "a"}, {"c", "b", "a"}} {
		gotIDs, gotAll := build(order)
		if !slices.Equal(firstIDs, gotIDs) || !reflect.DeepEqual(firstAll, gotAll) {
			t.Fatalf("insertion order %v changed the outcome:\n%v\n%v", order, firstIDs, gotIDs)
		}
	}
	if want := []string{"inferred:r1:a", "inferred:r1:b", "inferred:r1:c", "inferred:r2:a", "inferred:r2:b", "inferred:r2:c"}; !slices.Equal(firstIDs, want) {
		t.Errorf("derived order %v, want %v (rules in given order, subjects ascending, passes in sequence)", firstIDs, want)
	}
}

func TestNewValidatesRules(t *testing.T) {
	ok := simple("ok", "a", "b", 0.5)
	tests := []struct {
		name string
		rule infer.Rule
	}{
		{"empty ID", infer.Rule{Requires: ok.Requires, Produces: ok.Produces, Confidence: 0.5}},
		{"no requirements", infer.Rule{ID: "r", Produces: ok.Produces, Confidence: 0.5}},
		{"requirement without type", infer.Rule{ID: "r", Requires: []infer.Requirement{{}}, Produces: ok.Produces, Confidence: 0.5}},
		{"product without type", infer.Rule{ID: "r", Requires: ok.Requires, Confidence: 0.5}},
		{"confidence zero", infer.Rule{ID: "r", Requires: ok.Requires, Produces: ok.Produces}},
		{"confidence above one", infer.Rule{ID: "r", Requires: ok.Requires, Produces: ok.Produces, Confidence: 1.1}},
		{"negative ttl", infer.Rule{ID: "r", Requires: ok.Requires, Produces: infer.Product{Type: "b", TTL: -1}, Confidence: 0.5}},
		{"min confidence out of range", infer.Rule{ID: "r", Requires: []infer.Requirement{{Type: "a", MinConfidence: ptr(1.5)}}, Produces: ok.Produces, Confidence: 0.5}},
		{"all fixed without product subject", infer.Rule{ID: "r", Requires: []infer.Requirement{{Type: "a", Subject: "x"}}, Produces: ok.Produces, Confidence: 0.5}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := infer.New(infer.DefaultConfig(), []infer.Rule{tc.rule}); !errors.Is(err, infer.ErrInvalidRule) {
				t.Errorf("err = %v, want ErrInvalidRule", err)
			}
		})
	}
	if _, err := infer.New(infer.DefaultConfig(), []infer.Rule{ok, ok}); !errors.Is(err, infer.ErrInvalidRule) {
		t.Errorf("duplicate IDs: err = %v", err)
	}
	if _, err := infer.New(infer.DefaultConfig(), nil); err != nil {
		t.Errorf("no rules is allowed: %v", err)
	}
	_, err := infer.New(infer.Config{}, []infer.Rule{{}})
	if !errors.Is(err, infer.ErrInvalidConfig) || !errors.Is(err, infer.ErrInvalidRule) {
		t.Errorf("config and rule problems should be reported together: %v", err)
	}
}

func TestEngineCopiesItsRules(t *testing.T) {
	floor := 0.5
	rule := infer.Rule{ID: "r", Requires: []infer.Requirement{{Type: "a", MinConfidence: &floor, Sources: []string{"observed"}}}, Produces: infer.Product{Type: "b"}, Confidence: 0.5}
	rules := []infer.Rule{rule}
	e := engine(t, infer.DefaultConfig(), rules...)
	floor = 0.99 // caller mutates after New
	rules[0].Requires[0].Sources[0] = "tampered"
	rules[0].ID = "changed"
	s := store(t)
	put(t, s, "a", "x", 0.9)
	if res := run(t, e, s, 0); !slices.Equal(ids(res), []string{"inferred:r:x"}) {
		t.Errorf("engine was affected by caller mutation: %v", ids(res))
	}
}

func TestRunErrors(t *testing.T) {
	e := engine(t, infer.DefaultConfig(), simple("r", "a", "b", 0.5))
	if _, err := e.Run(nil, 0); !errors.Is(err, infer.ErrNilStore) {
		t.Errorf("nil store: err = %v", err)
	}
	// A derived ID that collides with a fact of another type is reported, not hidden.
	s := store(t)
	put(t, s, "a", "x", 0.9)
	s.Add(knowledge.Fact{ID: "inferred:r:x", Type: "unrelated", Subject: "x", Confidence: 0.1, Source: "derived"})
	if _, err := e.Run(s, 0); !errors.Is(err, knowledge.ErrIdentityMismatch) {
		t.Errorf("err = %v, want ErrIdentityMismatch", err)
	}
}

func TestResultPasses(t *testing.T) {
	s := store(t)
	res := run(t, engine(t, infer.DefaultConfig(), simple("r", "a", "b", 0.5)), s, 0)
	if res.Passes != 1 || len(res.Derived) != 0 {
		t.Errorf("empty store: %+v", res)
	}
}
