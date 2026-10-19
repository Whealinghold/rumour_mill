package declare_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Whealinghold/rumour_mill/declare"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/logic"
	"github.com/Whealinghold/rumour_mill/rules"
	"github.com/Whealinghold/rumour_mill/spread"
)

// Example_attributes builds a spreading filter and a rule from plain data, with
// one Lookup function as the only glue.
func Example_attributes() {
	// Spreading: gossip may not cross departments.
	enc := declare.NewCatalog[spread.Encounter]()
	must(enc.RegisterAttributeOps(declare.Attributes[spread.Encounter]{
		Sides: []string{"teller", "listener"},
		Lookup: func(e spread.Encounter, side, key string) (any, bool) {
			h := e.Teller
			if side == "listener" {
				h = e.Listener
			}
			v, ok := h.(*being).attrs[key]
			return v, ok
		},
	}, declare.AttributeNames{}))
	var filters []declare.Node
	must(json.Unmarshal([]byte(`[{"use":"attr_same","args":{"side":"teller","other_side":"listener","key":"department"}}]`), &filters))
	cond, err := enc.ConditionAll(filters)
	must(err)
	sp, err := spread.New(spread.DefaultConfig(), spread.WithFilter(cond))
	must(err)

	mk := func(id, dept string) *being {
		kb, err := knowledge.NewStore(knowledge.DefaultConfig())
		must(err)
		return &being{id: id, attrs: map[string]string{"department": dept}, kb: kb}
	}
	ann, bo, cy := mk("ann", "finance"), mk("bo", "finance"), mk("cy", "support")
	_, err = ann.kb.Add(knowledge.Fact{Type: "reorg", Subject: "office", Confidence: 0.9, Source: "observed"})
	must(err)
	res, err := sp.Spread(ann, []spread.Holder{bo, cy}, 1)
	must(err)
	fmt.Println("bo knows:", len(bo.kb.Find(knowledge.Query{}, 1)), "cy knows:", len(cy.kb.Find(knowledge.Query{}, 1)), "filtered:", res.Dropped.Filtered)

	// Rules: only night-shift staff react.
	sc := declare.NewCatalog[scene]()
	must(sc.RegisterAttributeOps(declare.Attributes[scene]{
		Sides:  []string{"entity"},
		Lookup: func(s scene, _, key string) (any, bool) { v, ok := s.self.attrs[key]; return v, ok },
	}, declare.AttributeNames{}))
	when, err := sc.Condition(declare.Node{Use: "attr_equals", Args: declare.Args{"side": "entity", "key": "role", "value": "night-shift"}})
	must(err)
	reg, err := rules.NewRegistry(rules.DefaultConfig(), rules.Layer[scene]{Name: "office"})
	must(err)
	must(reg.Register("office", "", rules.Rule[scene]{ID: "night-alert", Event: "alarm", When: when}))
	for _, role := range []string{"night-shift", "day-shift"} {
		r, err := reg.Process(scene{self: &being{attrs: map[string]string{"role": role}}}, "alarm")
		fmt.Println(role, fired(r, err))
	}

	// Output:
	// bo knows: 1 cy knows: 0 filtered: 1
	// night-shift [office/night-alert] err=<nil>
	// day-shift [] err=<nil>
}

type attrCtx map[string]map[string]any

func attrCat(t *testing.T, n declare.AttributeNames, extraSides ...string) (*declare.Catalog[attrCtx], error) {
	t.Helper()
	cat := declare.NewCatalog[attrCtx]()
	err := cat.RegisterAttributeOps(declare.Attributes[attrCtx]{
		Sides:  append([]string{"a", "b"}, extraSides...),
		Lookup: func(c attrCtx, side, key string) (any, bool) { v, ok := c[side][key]; return v, ok },
	}, n)
	return cat, err
}

func evalAttr(t *testing.T, cat *declare.Catalog[attrCtx], use string, args declare.Args, c attrCtx) bool {
	t.Helper()
	cond, err := cat.Condition(declare.Node{Use: use, Args: args})
	if err != nil {
		t.Fatalf("%s: %v", use, err)
	}
	return cond(c)
}

func TestAttributeRegistration(t *testing.T) {
	bad := declare.Attributes[attrCtx]{}
	if err := declare.NewCatalog[attrCtx]().RegisterAttributeOps(bad, declare.AttributeNames{}); !errors.Is(err, declare.ErrBadBindings) {
		t.Errorf("empty bindings: %v", err)
	}
	onlyLookup := declare.Attributes[attrCtx]{Lookup: func(attrCtx, string, string) (any, bool) { return nil, false }}
	if err := declare.NewCatalog[attrCtx]().RegisterAttributeOps(onlyLookup, declare.AttributeNames{}); !errors.Is(err, declare.ErrBadBindings) {
		t.Errorf("no sides: %v", err)
	}
	onlySides := declare.Attributes[attrCtx]{Sides: []string{"a"}}
	if err := declare.NewCatalog[attrCtx]().RegisterAttributeOps(onlySides, declare.AttributeNames{}); !errors.Is(err, declare.ErrBadBindings) {
		t.Errorf("no lookup: %v", err)
	}

	var names declare.AttributeNames
	if err := json.Unmarshal([]byte(`{"equals":"e","in":"i","same":"s"}`), &names); err != nil || names != (declare.AttributeNames{Equals: "e", In: "i", Same: "s"}) {
		t.Errorf("json names = %+v, %v", names, err)
	}

	if got := declare.DefaultAttributeNames(); got != (declare.AttributeNames{Equals: "attr_equals", In: "attr_in", Same: "attr_same"}) {
		t.Errorf("defaults = %+v", got)
	}

	// Rename (S14): new names load, old names are unknown.
	cat, err := attrCat(t, declare.AttributeNames{Equals: "is", In: "is_one_of", Same: "matches"})
	if err != nil {
		t.Fatal(err)
	}
	if !evalAttr(t, cat, "is", declare.Args{"side": "a", "key": "k", "value": "x"}, attrCtx{"a": {"k": "x"}}) {
		t.Error("renamed attr_equals should be true")
	}
	if _, err := cat.Condition(declare.Node{Use: "attr_equals", Args: declare.Args{"side": "a", "key": "k", "value": "x"}}); !errors.Is(err, declare.ErrUnknownName) {
		t.Errorf("old name: %v", err)
	}
	// A partially renamed set keeps the other defaults.
	cat, _ = attrCat(t, declare.AttributeNames{In: "one_of"})
	for _, name := range []string{"attr_equals", "one_of", "attr_same"} {
		if _, err := cat.Condition(declare.Node{Use: name, Args: declare.Args{"side": "a", "key": "k", "value": 1, "values": []any{1}, "other_side": "b"}}); errors.Is(err, declare.ErrUnknownName) {
			t.Errorf("%s not registered", name)
		}
	}

	// Failures register nothing.
	failures := map[string]declare.AttributeNames{
		"equal names": {Equals: "x", In: "x"},
		"taken":       {Same: "taken"},
		"reserved":    {In: "not"},
		"equals=same": {Equals: "x", Same: "x"},
		"in=same":     {In: "x", Same: "x"},
	}
	for name, n := range failures {
		cat := declare.NewCatalog[attrCtx]()
		if err := cat.RegisterCondition("taken", func(declare.Args) (logic.Condition[attrCtx], error) { return nil, nil }); err != nil {
			t.Fatal(err)
		}
		err := cat.RegisterAttributeOps(declare.Attributes[attrCtx]{Sides: []string{"a"}, Lookup: func(attrCtx, string, string) (any, bool) { return nil, false }}, n)
		if !errors.Is(err, declare.ErrDuplicateName) {
			t.Errorf("%s: err = %v", name, err)
		}
		if _, err := cat.Condition(declare.Node{Use: "attr_equals"}); !errors.Is(err, declare.ErrUnknownName) {
			t.Errorf("%s: attr_equals registered despite failure: %v", name, err)
		}
		// S7: a failed registration leaves the catalog usable.
		if err := cat.RegisterAttributeOps(declare.Attributes[attrCtx]{Sides: []string{"a"}, Lookup: func(attrCtx, string, string) (any, bool) { return nil, false }}, declare.AttributeNames{Equals: "ok_eq", In: "ok_in", Same: "ok_same"}); err != nil {
			t.Errorf("%s: valid registration after failure: %v", name, err)
		}
		if _, err := cat.Condition(declare.Node{Use: "ok_eq", Args: declare.Args{"side": "a", "key": "k", "value": 1}}); err != nil {
			t.Errorf("%s: valid node after failure: %v", name, err)
		}
	}
}

func TestAttributeSemantics(t *testing.T) {
	cat, _ := attrCat(t, declare.AttributeNames{})
	type holder struct{ v any }
	same := func(side, key string) declare.Args {
		return declare.Args{"side": side, "key": key, "other_side": "b"}
	}
	var jn any
	_ = json.Unmarshal([]byte(`3`), &jn) // float64(3), as loaded
	tests := []struct {
		name string
		use  string
		args declare.Args
		ctx  attrCtx
		want bool
	}{
		{"string equal", "attr_equals", declare.Args{"side": "a", "key": "k", "value": "x"}, attrCtx{"a": {"k": "x"}}, true},
		{"string differ", "attr_equals", declare.Args{"side": "a", "key": "k", "value": "x"}, attrCtx{"a": {"k": "y"}}, false},
		{"missing key", "attr_equals", declare.Args{"side": "a", "key": "k", "value": "x"}, attrCtx{"a": {}}, false},
		{"int vs json number", "attr_equals", declare.Args{"side": "a", "key": "k", "value": jn}, attrCtx{"a": {"k": 3}}, true},
		{"float vs yaml int", "attr_equals", declare.Args{"side": "a", "key": "k", "value": 3}, attrCtx{"a": {"k": 3.0}}, true},
		{"uint vs int", "attr_equals", declare.Args{"side": "a", "key": "k", "value": 3}, attrCtx{"a": {"k": uint8(3)}}, true},
		{"number vs string", "attr_equals", declare.Args{"side": "a", "key": "k", "value": "3"}, attrCtx{"a": {"k": 3}}, false},
		{"bool", "attr_equals", declare.Args{"side": "a", "key": "k", "value": true}, attrCtx{"a": {"k": true}}, true},
		{"nil equals nil", "attr_equals", declare.Args{"side": "a", "key": "k", "value": nil}, attrCtx{"a": {"k": nil}}, true},
		{"nil vs value", "attr_equals", declare.Args{"side": "a", "key": "k", "value": nil}, attrCtx{"a": {"k": "x"}}, false},
		{"slice attribute", "attr_equals", declare.Args{"side": "a", "key": "k", "value": "x"}, attrCtx{"a": {"k": []string{"x"}}}, false},
		{"slice vs slice", "attr_equals", declare.Args{"side": "a", "key": "k", "value": []any{"x"}}, attrCtx{"a": {"k": []any{"x"}}}, false},
		{"interface field holding slice", "attr_equals", declare.Args{"side": "a", "key": "k", "value": holder{[]int{1}}}, attrCtx{"a": {"k": holder{[]int{1}}}}, false},
		{"interface field comparable", "attr_equals", declare.Args{"side": "a", "key": "k", "value": holder{1}}, attrCtx{"a": {"k": holder{1}}}, true},
		{"in match", "attr_in", declare.Args{"side": "a", "key": "k", "values": []any{"x", "y"}}, attrCtx{"a": {"k": "y"}}, true},
		{"in go slice", "attr_in", declare.Args{"side": "a", "key": "k", "values": []string{"x", "y"}}, attrCtx{"a": {"k": "x"}}, true},
		{"in numbers", "attr_in", declare.Args{"side": "a", "key": "k", "values": []any{1.0, 2.0}}, attrCtx{"a": {"k": 2}}, true},
		{"in miss", "attr_in", declare.Args{"side": "a", "key": "k", "values": []any{"x"}}, attrCtx{"a": {"k": "z"}}, false},
		{"in missing key", "attr_in", declare.Args{"side": "a", "key": "k", "values": []any{nil}}, attrCtx{"a": {}}, false},
		{"same", "attr_same", same("a", "k"), attrCtx{"a": {"k": 1}, "b": {"k": 1.0}}, true},
		{"same differ", "attr_same", same("a", "k"), attrCtx{"a": {"k": "x"}, "b": {"k": "y"}}, false},
		{"same missing self", "attr_same", same("a", "k"), attrCtx{"a": {}, "b": {"k": "y"}}, false},
		{"same missing other", "attr_same", same("a", "k"), attrCtx{"a": {"k": "y"}, "b": {}}, false},
		{"same both missing", "attr_same", same("a", "k"), attrCtx{"a": {}, "b": {}}, false},
		{"other_key", "attr_same", declare.Args{"side": "a", "key": "k", "other_side": "b", "other_key": "j"}, attrCtx{"a": {"k": "x"}, "b": {"j": "x"}}, true},
		{"other_key defaults to key", "attr_same", declare.Args{"side": "a", "key": "k", "other_side": "b"}, attrCtx{"a": {"k": "x"}, "b": {"j": "x"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalAttr(t, cat, tc.use, tc.args, tc.ctx); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}

	// R2.6: not over a missing attribute is true.
	not, err := cat.Condition(declare.Node{Use: "not", Of: []declare.Node{{Use: "attr_equals", Args: declare.Args{"side": "a", "key": "k", "value": "x"}}}})
	if err != nil || !not(attrCtx{"a": {}}) {
		t.Errorf("not(missing): %v", err)
	}
}

func TestAttributeLoadErrors(t *testing.T) {
	cat, _ := attrCat(t, declare.AttributeNames{})
	tests := map[string]declare.Node{
		"unknown side":   {Use: "attr_equals", Args: declare.Args{"side": "evnt", "key": "k", "value": 1}},
		"missing side":   {Use: "attr_equals", Args: declare.Args{"key": "k", "value": 1}},
		"missing key":    {Use: "attr_equals", Args: declare.Args{"side": "a", "value": 1}},
		"missing value":  {Use: "attr_equals", Args: declare.Args{"side": "a", "key": "k"}},
		"unknown arg":    {Use: "attr_equals", Args: declare.Args{"side": "a", "key": "k", "value": 1, "vlaue": 1}},
		"missing values": {Use: "attr_in", Args: declare.Args{"side": "a", "key": "k"}},
		"empty values":   {Use: "attr_in", Args: declare.Args{"side": "a", "key": "k", "values": []any{}}},
		"scalar values":  {Use: "attr_in", Args: declare.Args{"side": "a", "key": "k", "values": "x"}},
		"unknown in arg": {Use: "attr_in", Args: declare.Args{"side": "a", "key": "k", "values": []any{1}, "value": 1}},
		"same no other":  {Use: "attr_same", Args: declare.Args{"side": "a", "key": "k"}},
		"same bad other": {Use: "attr_same", Args: declare.Args{"side": "a", "key": "k", "other_side": "zz"}},
		"same bad key":   {Use: "attr_same", Args: declare.Args{"side": "a", "key": 1, "other_side": "b"}},
		"same unknown":   {Use: "attr_same", Args: declare.Args{"side": "a", "key": "k", "other_side": "b", "x": 1}},
		"nested path":    {Use: "all", Of: []declare.Node{{Use: "attr_equals", Args: declare.Args{"side": "evnt", "key": "k", "value": 1}}}},
	}
	for name, n := range tests {
		_, err := cat.Condition(n)
		if !errors.Is(err, declare.ErrBadArgs) {
			t.Errorf("%s: err = %v, want ErrBadArgs", name, err)
		}
	}
	_, err := cat.Condition(tests["nested path"])
	if err == nil || err.Error()[:len("node.of[0]")] != "node.of[0]" {
		t.Errorf("path missing: %v", err)
	}
}

func TestAttributeSidesCopied(t *testing.T) {
	sides := []string{"a"}
	cat := declare.NewCatalog[attrCtx]()
	if err := cat.RegisterAttributeOps(declare.Attributes[attrCtx]{Sides: sides, Lookup: func(attrCtx, string, string) (any, bool) { return nil, false }}, declare.AttributeNames{}); err != nil {
		t.Fatal(err)
	}
	sides[0] = "z"
	if _, err := cat.Condition(declare.Node{Use: "attr_equals", Args: declare.Args{"side": "a", "key": "k", "value": 1}}); err != nil {
		t.Errorf("caller mutation leaked: %v", err)
	}
}

func TestAttributeConcurrent(t *testing.T) {
	cat, _ := attrCat(t, declare.AttributeNames{})
	cond, err := cat.Condition(declare.Node{Use: "attr_same", Args: declare.Args{"side": "a", "key": "k", "other_side": "b"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := attrCtx{"a": {"k": 1}, "b": {"k": 1}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if !cond(ctx) {
					t.Error("expected true")
				}
			}
		}()
	}
	wg.Wait()
}

func TestAttributeEncounterCatalog(t *testing.T) {
	enc := declare.NewCatalog[spread.Encounter]()
	if err := enc.RegisterAttributeOps(declare.Attributes[spread.Encounter]{
		Sides: []string{"teller", "listener"},
		Lookup: func(e spread.Encounter, side, key string) (any, bool) {
			h := e.Teller
			if side == "listener" {
				h = e.Listener
			}
			v, ok := h.(*being).attrs[key]
			return v, ok
		},
	}, declare.AttributeNames{}); err != nil {
		t.Fatal(err)
	}
	cond, err := enc.Condition(declare.Node{Use: "attr_same", Args: declare.Args{"side": "teller", "other_side": "listener", "key": "department"}})
	if err != nil {
		t.Fatal(err)
	}
	sp, err := spread.New(spread.DefaultConfig(), spread.WithFilter(cond))
	if err != nil {
		t.Fatal(err)
	}
	mk := func(id, dept string) *being {
		kb, err := knowledge.NewStore(knowledge.DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		return &being{id: id, attrs: map[string]string{"department": dept}, kb: kb}
	}
	ann, bo, cy := mk("ann", "finance"), mk("bo", "finance"), mk("cy", "support")
	if _, err := ann.kb.Add(knowledge.Fact{Type: "reorg", Subject: "office", Confidence: 0.9, Source: "observed"}); err != nil {
		t.Fatal(err)
	}
	res, err := sp.Spread(ann, []spread.Holder{bo, cy}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Dropped.Filtered != 1 || len(bo.kb.All()) != 1 || len(cy.kb.All()) != 0 {
		t.Errorf("filtered %d, bo %d, cy %d", res.Dropped.Filtered, len(bo.kb.All()), len(cy.kb.All()))
	}
}

func TestAttributeRulesContext(t *testing.T) {
	sc := declare.NewCatalog[scene]()
	if err := sc.RegisterAttributeOps(declare.Attributes[scene]{
		Sides:  []string{"entity"},
		Lookup: func(s scene, _, key string) (any, bool) { v, ok := s.self.attrs[key]; return v, ok },
	}, declare.AttributeNames{}); err != nil {
		t.Fatal(err)
	}
	when, err := sc.Condition(declare.Node{Use: "attr_equals", Args: declare.Args{"side": "entity", "key": "role", "value": "night-shift"}})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := rules.NewRegistry(rules.DefaultConfig(), rules.Layer[scene]{Name: "office"})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Register("office", "", rules.Rule[scene]{ID: "night-alert", Event: "alarm", When: when}); err != nil {
		t.Fatal(err)
	}
	for role, want := range map[string]int{"night-shift": 1, "day-shift": 0} {
		r, err := reg.Process(scene{self: &being{attrs: map[string]string{"role": role}}}, "alarm")
		if err != nil || len(r.Fired) != want {
			t.Errorf("%s: fired %d, err %v, want %d", role, len(r.Fired), err, want)
		}
	}
}
