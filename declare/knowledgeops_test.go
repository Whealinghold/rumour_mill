package declare_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Whealinghold/rumour_mill/declare"
	"github.com/Whealinghold/rumour_mill/knowledge"
)

func sceneFor(t *testing.T, subject string) scene {
	t.Helper()
	kb, err := knowledge.NewStore(knowledge.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return scene{self: &being{id: "me", attrs: map[string]string{}, kb: kb}, subject: subject, evtID: "ev-1", now: 10}
}

func action(t *testing.T, a declare.Args) func(scene) error {
	t.Helper()
	act, err := sceneCatalog().Action(declare.Node{Use: "add_fact", Args: a})
	if err != nil {
		t.Fatalf("add_fact %v: %v", a, err)
	}
	return act
}

func TestAddFact(t *testing.T) {
	base := func(extra declare.Args) declare.Args {
		a := declare.Args{"type": "t", "confidence": 0.7, "source": "observed"}
		for k, v := range extra {
			a[k] = v
		}
		return a
	}
	s := sceneFor(t, "ship-1")
	if err := action(t, base(declare.Args{"ttl": 100, "private": true}))(s); err != nil {
		t.Fatal(err)
	}
	facts := s.self.kb.All()
	exp := knowledge.Tick(110)
	if len(facts) != 1 {
		t.Fatalf("%d facts", len(facts))
	}
	f := facts[0]
	if f.Type != "t" || f.Subject != "ship-1" || f.Confidence != 0.7 || f.Source != "observed" || f.Observed != 10 ||
		!f.Private || f.Expiry == nil || *f.Expiry != exp || f.Prov.EventID != "ev-1" || f.Discriminator != "" {
		t.Errorf("fact = %+v", f)
	}

	// Without per_event repeats merge; with it each event is separate.
	merge := sceneFor(t, "ship-1")
	for _, ev := range []string{"ev-1", "ev-2"} {
		merge.evtID = ev
		action(t, base(nil))(merge)
	}
	if n := len(merge.self.kb.All()); n != 1 {
		t.Errorf("repeats without per_event: %d facts, want 1", n)
	}
	per := sceneFor(t, "ship-1")
	for _, ev := range []string{"ev-1", "ev-2", "ev-2"} {
		per.evtID = ev
		action(t, base(declare.Args{"per_event": true}))(per)
	}
	if n := len(per.self.kb.All()); n != 2 {
		t.Errorf("per_event: %d facts, want 2 (one per distinct event)", n)
	}

	// No ttl means no expiry.
	plain := sceneFor(t, "x")
	action(t, base(nil))(plain)
	if plain.self.kb.All()[0].Expiry != nil {
		t.Error("expiry set without ttl")
	}
}

func TestAddFactErrors(t *testing.T) {
	valid := func() declare.Args { return declare.Args{"type": "t", "confidence": 0.7, "source": "observed"} }
	with := func(k string, v any) declare.Args { a := valid(); a[k] = v; return a }
	without := func(k string) declare.Args { a := valid(); delete(a, k); return a }
	tests := []struct {
		name string
		args declare.Args
	}{
		{"missing type", without("type")},
		{"missing source", without("source")},
		{"missing confidence", without("confidence")},
		{"confidence above 1", with("confidence", 1.5)},
		{"confidence below 0", with("confidence", -0.1)},
		{"confidence not a number", with("confidence", "high")},
		{"negative ttl", with("ttl", -1)},
		{"fractional ttl", with("ttl", 1.5)},
		{"private not a bool", with("private", "yes")},
		{"per_event not a bool", with("per_event", 1)},
		{"unknown arg", with("typo", true)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sceneCatalog().Action(declare.Node{Use: "add_fact", Args: tc.args})
			if !errors.Is(err, declare.ErrBadArgs) {
				t.Errorf("err = %v, want ErrBadArgs", err)
			}
		})
	}
}

func TestAddFactNeedsAStoreAndEventID(t *testing.T) {
	s := sceneFor(t, "x")
	s.self.kb = nil
	if err := action(t, declare.Args{"type": "t", "confidence": 1, "source": "observed"})(s); !errors.Is(err, declare.ErrNoStore) {
		t.Errorf("err = %v, want ErrNoStore", err)
	}
	// per_event with no EventID binding is rejected when the rule is built.
	cat := declare.NewCatalog[scene]()
	cat.RegisterKnowledgeOps(declare.Bindings[scene]{
		Store:   func(s scene) *knowledge.Store { return s.self.kb },
		Now:     func(s scene) knowledge.Tick { return s.now },
		Subject: func(s scene) string { return s.subject },
	})
	_, err := cat.Action(declare.Node{Use: "add_fact", Args: declare.Args{"type": "t", "confidence": 1, "source": "observed", "per_event": true}})
	if !errors.Is(err, declare.ErrBadArgs) {
		t.Errorf("per_event without EventID: err = %v", err)
	}
	// Without per_event, EventID is optional and provenance is simply empty.
	act, err := cat.Action(declare.Node{Use: "add_fact", Args: declare.Args{"type": "t", "confidence": 1, "source": "observed"}})
	s2 := sceneFor(t, "x")
	if err != nil || act(s2) != nil || s2.self.kb.All()[0].Prov.EventID != "" {
		t.Errorf("optional EventID: %v", err)
	}
}

func TestKnows(t *testing.T) {
	setup := func() scene {
		s := sceneFor(t, "ship-1")
		add := func(typ, subject, source string, c float64) {
			s.self.kb.Add(knowledge.Fact{Type: typ, Subject: subject, Confidence: c, Source: source})
		}
		add("seen", "ship-1", "observed", 0.9)
		add("seen", "ship-2", "relayed", 0.5)
		add("weak", "ship-1", "observed", 0.25)
		add("empty-subject", "", "observed", 0.9)
		return s
	}
	tests := []struct {
		name string
		args declare.Args
		want bool
	}{
		{"by type", declare.Args{"type": "seen"}, true},
		{"unknown type", declare.Args{"type": "nope"}, false},
		{"about the subject", declare.Args{"type": "seen", "about_subject": true}, true},
		{"about the subject, other facts only", declare.Args{"type": "weak", "about_subject": false}, true},
		{"min confidence passes", declare.Args{"type": "seen", "about_subject": true, "min_confidence": 0.9}, true},
		{"min confidence fails (>= not >)", declare.Args{"type": "seen", "about_subject": true, "min_confidence": 0.95}, false},
		{"default floor is 0.2, so 0.25 is visible", declare.Args{"type": "weak"}, true},
		{"explicit floor above it", declare.Args{"type": "weak", "min_confidence": 0.3}, false},
		{"source as string", declare.Args{"type": "seen", "source": "relayed"}, true},
		{"source as list", declare.Args{"type": "seen", "source": []any{"relayed", "observed"}}, true},
		{"source excludes", declare.Args{"type": "seen", "about_subject": true, "source": "relayed"}, false},
		{"empty subject does not match a real one", declare.Args{"type": "empty-subject", "about_subject": true}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := sceneCatalog().Condition(declare.Node{Use: "knows", Args: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			if got := c(setup()); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
	// An empty bound subject matches only facts with an empty subject.
	c, _ := sceneCatalog().Condition(declare.Node{Use: "knows", Args: declare.Args{"type": "empty-subject", "about_subject": true}})
	s := setup()
	s.subject = ""
	if !c(s) {
		t.Error("about_subject with an empty subject should match an empty-subject fact")
	}
	// A context with no store knows nothing.
	s.self.kb = nil
	if c(s) {
		t.Error("nil store should be false")
	}
}

func TestKnowsErrors(t *testing.T) {
	tests := []struct {
		name string
		args declare.Args
	}{
		{"missing type", declare.Args{}},
		{"type wrong kind", declare.Args{"type": 3}},
		{"about_subject wrong kind", declare.Args{"type": "t", "about_subject": "yes"}},
		{"min_confidence out of range", declare.Args{"type": "t", "min_confidence": 2}},
		{"min_confidence wrong kind", declare.Args{"type": "t", "min_confidence": "x"}},
		{"source wrong kind", declare.Args{"type": "t", "source": 5}},
		{"unknown arg", declare.Args{"type": "t", "abot_subject": true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := sceneCatalog().Condition(declare.Node{Use: "knows", Args: tc.args}); !errors.Is(err, declare.ErrBadArgs) {
				t.Errorf("err = %v, want ErrBadArgs", err)
			}
		})
	}
}

func TestRegisterKnowledgeOpsErrors(t *testing.T) {
	full := declare.Bindings[scene]{
		Store:   func(s scene) *knowledge.Store { return s.self.kb },
		Now:     func(s scene) knowledge.Tick { return s.now },
		Subject: func(s scene) string { return s.subject },
	}
	for name, mutate := range map[string]func(*declare.Bindings[scene]){
		"no Store":   func(b *declare.Bindings[scene]) { b.Store = nil },
		"no Now":     func(b *declare.Bindings[scene]) { b.Now = nil },
		"no Subject": func(b *declare.Bindings[scene]) { b.Subject = nil },
	} {
		b := full
		mutate(&b)
		if err := declare.NewCatalog[scene]().RegisterKnowledgeOps(b); !errors.Is(err, declare.ErrBadBindings) {
			t.Errorf("%s: err = %v, want ErrBadBindings", name, err)
		}
	}
	cat := declare.NewCatalog[scene]()
	if err := cat.RegisterKnowledgeOps(full); err != nil {
		t.Fatal(err)
	}
	if err := cat.RegisterKnowledgeOps(full); !errors.Is(err, declare.ErrDuplicateName) {
		t.Errorf("registering twice: err = %v, want ErrDuplicateName", err)
	}
}

func discCatalog(t *testing.T, names declare.ArgNames) *declare.Catalog[scene] {
	t.Helper()
	cat := declare.NewCatalog[scene]()
	err := cat.RegisterKnowledgeOps(declare.Bindings[scene]{
		Store:    func(s scene) *knowledge.Store { return s.self.kb },
		Now:      func(s scene) knowledge.Tick { return s.now },
		Subject:  func(s scene) string { return s.subject },
		EventID:  func(s scene) string { return s.evtID },
		ArgNames: names,
		Discriminators: map[string]func(scene) string{
			"place": func(s scene) string { return s.place },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func discArgs(extra declare.Args) declare.Args {
	a := declare.Args{"type": "t", "confidence": 0.7, "source": "observed"}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

func TestDiscriminator(t *testing.T) {
	run := func(cat *declare.Catalog[scene], extra declare.Args, places ...string) []knowledge.Fact {
		act, err := cat.Action(declare.Node{Use: "add_fact", Args: discArgs(extra)})
		if err != nil {
			t.Fatal(err)
		}
		s := sceneFor(t, "x")
		for _, p := range places {
			s.place = p
			if err := act(s); err != nil {
				t.Fatal(err)
			}
		}
		return s.self.kb.All()
	}
	cat := discCatalog(t, declare.ArgNames{})
	if f := run(cat, declare.Args{"discriminator": "k"}, "a"); len(f) != 1 || f[0].Discriminator != "k" {
		t.Errorf("literal: %+v", f)
	}
	if f := run(cat, declare.Args{"discriminator_from": "place"}, "a", "b"); len(f) != 2 {
		t.Errorf("two sources, %d facts, want 2", len(f))
	}
	if f := run(cat, declare.Args{"discriminator_from": "place"}, "a", "a"); len(f) != 1 {
		t.Errorf("same source, %d facts, want 1", len(f))
	}
	f := run(cat, declare.Args{"discriminator_from": "place"}, "")
	g := run(cat, nil, "")
	if len(f) != 1 || len(g) != 1 || f[0].ID != g[0].ID {
		t.Errorf("empty result should be default identity: %+v vs %+v", f, g)
	}
	if f := run(cat, declare.Args{"per_event": true}, "a"); len(f) != 1 || f[0].Discriminator != "ev-1" {
		t.Errorf("per_event unchanged: %+v", f)
	}
	act, _ := cat.Action(declare.Node{Use: "add_fact", Args: discArgs(declare.Args{"discriminator": "a\x1fb"})})
	if err := act(sceneFor(t, "x")); !errors.Is(err, knowledge.ErrInvalidFact) {
		t.Errorf("U+001F: err = %v, want ErrInvalidFact", err)
	}

	for name, extra := range map[string]declare.Args{
		"unknown source": {"discriminator_from": "nope"},
		"literal+from":   {"discriminator": "k", "discriminator_from": "place"},
		"literal+event":  {"discriminator": "k", "per_event": true},
		"from+event":     {"discriminator_from": "place", "per_event": true},
		"literal type":   {"discriminator": 3},
		"from type":      {"discriminator_from": 3},
	} {
		if _, err := cat.Action(declare.Node{Use: "add_fact", Args: discArgs(extra)}); !errors.Is(err, declare.ErrBadArgs) {
			t.Errorf("%s: err = %v, want ErrBadArgs", name, err)
		}
	}
}

func TestArgNames(t *testing.T) {
	if d := declare.DefaultArgNames(); d.Discriminator != "discriminator" || d.DiscriminatorFrom != "discriminator_from" {
		t.Errorf("defaults = %+v", d)
	}
	cat := discCatalog(t, declare.ArgNames{Discriminator: "key"})
	if _, err := cat.Action(declare.Node{Use: "add_fact", Args: discArgs(declare.Args{"key": "k"})}); err != nil {
		t.Errorf("renamed arg: %v", err)
	}
	if _, err := cat.Action(declare.Node{Use: "add_fact", Args: discArgs(declare.Args{"discriminator": "k"})}); !errors.Is(err, declare.ErrBadArgs) {
		t.Errorf("old name: err = %v, want ErrBadArgs", err)
	}
	if _, err := cat.Action(declare.Node{Use: "add_fact", Args: discArgs(declare.Args{"discriminator_from": "place"})}); err != nil {
		t.Errorf("unrenamed field keeps default: %v", err)
	}

	full := declare.Bindings[scene]{
		Store:   func(s scene) *knowledge.Store { return s.self.kb },
		Now:     func(s scene) knowledge.Tick { return s.now },
		Subject: func(s scene) string { return s.subject },
	}
	for name, mutate := range map[string]func(*declare.Bindings[scene]){
		"nil source": func(b *declare.Bindings[scene]) { b.Discriminators = map[string]func(scene) string{"x": nil} },
		"equal names": func(b *declare.Bindings[scene]) {
			b.ArgNames = declare.ArgNames{Discriminator: "z", DiscriminatorFrom: "z"}
		},
		"base collision":  func(b *declare.Bindings[scene]) { b.ArgNames = declare.ArgNames{Discriminator: "ttl"} },
		"knows collision": func(b *declare.Bindings[scene]) { b.ArgNames = declare.ArgNames{DiscriminatorFrom: "min_confidence"} },
	} {
		b := full
		mutate(&b)
		if err := declare.NewCatalog[scene]().RegisterKnowledgeOps(b); !errors.Is(err, declare.ErrBadBindings) {
			t.Errorf("%s: err = %v, want ErrBadBindings", name, err)
		}
	}
}

func discCatalogWith(t *testing.T, names declare.ArgNames, src map[string]func(scene) string) *declare.Catalog[scene] {
	t.Helper()
	cat := declare.NewCatalog[scene]()
	err := cat.RegisterKnowledgeOps(declare.Bindings[scene]{
		Store:          func(s scene) *knowledge.Store { return s.self.kb },
		Now:            func(s scene) knowledge.Tick { return s.now },
		Subject:        func(s scene) string { return s.subject },
		ArgNames:       names,
		Discriminators: src,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func TestDiscriminatorsMapCopied(t *testing.T) {
	src := map[string]func(scene) string{"place": func(s scene) string { return s.place }}
	cat := discCatalogWith(t, declare.ArgNames{}, src)
	delete(src, "place")
	src["place"] = func(scene) string { return "hijacked" }
	act, err := cat.Action(declare.Node{Use: "add_fact", Args: discArgs(declare.Args{"discriminator_from": "place"})})
	if err != nil {
		t.Fatalf("deleting from the caller's map broke a later load: %v", err)
	}
	s := sceneFor(t, "x")
	s.place = "real"
	if err := act(s); err != nil {
		t.Fatal(err)
	}
	if f := s.self.kb.All(); len(f) != 1 || f[0].Discriminator != "real" {
		t.Errorf("caller's replacement leaked: %+v", f)
	}
}

func TestUnknownSourceListsSorted(t *testing.T) {
	cat := discCatalogWith(t, declare.ArgNames{}, map[string]func(scene) string{
		"zeta":  func(scene) string { return "z" },
		"alpha": func(scene) string { return "a" },
	})
	_, err := cat.Action(declare.Node{Use: "add_fact", Args: discArgs(declare.Args{"discriminator_from": "nope"})})
	if !errors.Is(err, declare.ErrBadArgs) {
		t.Fatalf("err = %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "[alpha zeta]") {
		t.Errorf("sources not listed sorted: %s", msg)
	}
}

func TestRenamedDiscriminatorRuns(t *testing.T) {
	cat := discCatalogWith(t, declare.ArgNames{Discriminator: "id_part", DiscriminatorFrom: "id_part_from"},
		map[string]func(scene) string{"place": func(s scene) string { return s.place }})
	run := func(extra declare.Args, places ...string) []knowledge.Fact {
		act, err := cat.Action(declare.Node{Use: "add_fact", Args: discArgs(extra)})
		if err != nil {
			t.Fatal(err)
		}
		s := sceneFor(t, "x")
		for _, p := range places {
			s.place = p
			if err := act(s); err != nil {
				t.Fatal(err)
			}
		}
		return s.self.kb.All()
	}
	if f := run(declare.Args{"id_part_from": "place"}, "a", "b"); len(f) != 2 {
		t.Errorf("different values: %d facts, want 2", len(f))
	}
	if f := run(declare.Args{"id_part_from": "place"}, "a", "a"); len(f) != 1 {
		t.Errorf("same value: %d facts, want 1", len(f))
	}
	if f := run(declare.Args{"id_part": "k"}, "a"); len(f) != 1 || f[0].Discriminator != "k" {
		t.Errorf("renamed literal: %+v", f)
	}
	for _, old := range []declare.Args{{"discriminator": "k"}, {"discriminator_from": "place"}} {
		if _, err := cat.Action(declare.Node{Use: "add_fact", Args: discArgs(old)}); !errors.Is(err, declare.ErrBadArgs) {
			t.Errorf("old name %v: err = %v, want ErrBadArgs", old, err)
		}
	}

	// Renaming only one leaves the other default working.
	onlyLit := discCatalogWith(t, declare.ArgNames{Discriminator: "id_part"},
		map[string]func(scene) string{"place": func(s scene) string { return s.place }})
	if _, err := onlyLit.Action(declare.Node{Use: "add_fact", Args: discArgs(declare.Args{"discriminator_from": "place"})}); err != nil {
		t.Errorf("default discriminator_from after renaming the other: %v", err)
	}
	onlyFrom := discCatalogWith(t, declare.ArgNames{DiscriminatorFrom: "id_part_from"}, nil)
	if _, err := onlyFrom.Action(declare.Node{Use: "add_fact", Args: discArgs(declare.Args{"discriminator": "k"})}); err != nil {
		t.Errorf("default discriminator after renaming the other: %v", err)
	}
}
