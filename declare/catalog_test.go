package declare_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Whealinghold/rumour_mill/declare"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/logic"
)

// cx is a minimal context for catalog tests.
type cx struct {
	n   int
	log *[]string
	kb  *knowledge.Store
}

func newCx(n int) cx {
	kb, _ := knowledge.NewStore(knowledge.DefaultConfig())
	return cx{n: n, log: new([]string), kb: kb}
}

func testCatalog(t testing.TB) *declare.Catalog[cx] {
	t.Helper()
	cat := declare.NewCatalog[cx]()
	mustReg := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	mustReg(cat.RegisterCondition("gt", func(a declare.Args) (logic.Condition[cx], error) {
		v, err := a.Int("value")
		if err != nil {
			return nil, err
		}
		return func(c cx) bool { return int64(c.n) > v }, a.Unknown("value")
	}))
	mustReg(cat.RegisterCondition("even", func(a declare.Args) (logic.Condition[cx], error) {
		return func(c cx) bool { return c.n%2 == 0 }, a.Unknown()
	}))
	mustReg(cat.RegisterAction("say", func(a declare.Args) (logic.Action[cx], error) {
		msg, err := a.String("msg")
		if err != nil {
			return nil, err
		}
		return func(c cx) error { *c.log = append(*c.log, msg); return nil }, a.Unknown("msg")
	}))
	mustReg(cat.RegisterAction("fail", func(a declare.Args) (logic.Action[cx], error) {
		return func(cx) error { return errors.New("failed") }, nil
	}))
	// "shared" exists in both namespaces: they are separate.
	mustReg(cat.RegisterCondition("shared", func(declare.Args) (logic.Condition[cx], error) { return func(cx) bool { return true }, nil }))
	mustReg(cat.RegisterAction("shared", func(declare.Args) (logic.Action[cx], error) { return func(cx) error { return nil }, nil }))
	mustReg(cat.RegisterKnowledgeOps(declare.Bindings[cx]{
		Store:          func(c cx) *knowledge.Store { return c.kb },
		Now:            func(c cx) knowledge.Tick { return knowledge.Tick(c.n) },
		Subject:        func(cx) string { return "s" },
		Discriminators: map[string]func(cx) string{"parity": func(c cx) string { return fmt.Sprint(c.n % 2) }},
	}))
	mustReg(cat.RegisterAttributeOps(declare.Attributes[cx]{Sides: []string{"self", "peer"}, Lookup: func(c cx, side, k string) (any, bool) {
		if k != "n" {
			return nil, false
		}
		if side == "peer" {
			return c.n + 1, true
		}
		return c.n, true
	}}, declare.AttributeNames{}))
	return cat
}

func node(use string, args declare.Args, of ...declare.Node) declare.Node {
	return declare.Node{Use: use, Args: args, Of: of}
}

func TestRegisterErrors(t *testing.T) {
	cond := func(declare.Args) (logic.Condition[cx], error) { return nil, nil }
	act := func(declare.Args) (logic.Action[cx], error) { return nil, nil }
	cat := declare.NewCatalog[cx]()
	cat.RegisterCondition("taken", cond)
	cat.RegisterAction("taken-action", act)

	tests := []struct {
		name string
		err  error
		want error
	}{
		{"condition ok", cat.RegisterCondition("fresh", cond), nil},
		{"condition empty name", cat.RegisterCondition("", cond), declare.ErrBadName},
		{"condition nil factory", cat.RegisterCondition("nilf", nil), declare.ErrBadName},
		{"condition duplicate", cat.RegisterCondition("taken", cond), declare.ErrDuplicateName},
		{"action ok", cat.RegisterAction("fresh", act), nil}, // separate namespace from conditions
		{"action empty name", cat.RegisterAction("", act), declare.ErrBadName},
		{"action nil factory", cat.RegisterAction("nilf", nil), declare.ErrBadName},
		{"action duplicate", cat.RegisterAction("taken-action", act), declare.ErrDuplicateName},
	}
	for _, name := range []string{"all", "any", "not", "seq", "if"} {
		tests = append(tests,
			struct {
				name string
				err  error
				want error
			}{"condition reserved " + name, cat.RegisterCondition(name, cond), declare.ErrDuplicateName},
			struct {
				name string
				err  error
				want error
			}{"action reserved " + name, cat.RegisterAction(name, act), declare.ErrDuplicateName})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.want) && !(tc.err == nil && tc.want == nil) {
				t.Errorf("err = %v, want %v", tc.err, tc.want)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	cat := testCatalog(t)
	ok := node("even", nil)
	tests := []struct {
		name     string
		n        declare.Node
		asAction bool
		want     error
		path     string // must appear in the message
	}{
		{"empty use", declare.Node{}, false, declare.ErrBadNode, "node:"},
		{"empty use as action", declare.Node{}, true, declare.ErrBadNode, "node:"},
		{"unknown condition", node("evn", nil), false, declare.ErrUnknownName, `condition "evn"`},
		{"unknown action", node("sya", nil), true, declare.ErrUnknownName, `action "sya"`},
		{"action used as condition", node("say", declare.Args{"msg": "x"}), false, declare.ErrWrongKind, "node:"},
		{"condition used as action", node("even", nil), true, declare.ErrWrongKind, "node:"},
		{"seq in condition position", node("seq", nil), false, declare.ErrWrongKind, "node:"},
		{"if in condition position", declare.Node{Use: "if"}, false, declare.ErrWrongKind, "node:"},
		{"all in action position", node("all", nil), true, declare.ErrWrongKind, "node:"},
		{"any in action position", node("any", nil), true, declare.ErrWrongKind, "node:"},
		{"not in action position", node("not", nil, ok), true, declare.ErrWrongKind, "node:"},
		{"not with no child", node("not", nil), false, declare.ErrBadNode, "node:"},
		{"not with two children", node("not", nil, ok, ok), false, declare.ErrBadNode, "node:"},
		{"all with args", node("all", declare.Args{"x": 1}), false, declare.ErrBadNode, "node:"},
		{"all with if fields", declare.Node{Use: "all", Cond: &declare.Node{Use: "even"}}, false, declare.ErrBadNode, "node:"},
		{"registered name with children", node("even", nil, ok), false, declare.ErrBadNode, "node:"},
		{"registered action with if fields", declare.Node{Use: "say", Args: declare.Args{"msg": "x"}, Then: &declare.Node{Use: "fail"}}, true, declare.ErrBadNode, "node:"},
		{"seq with args", node("seq", declare.Args{"x": 1}), true, declare.ErrBadNode, "node:"},
		{"if without cond", declare.Node{Use: "if", Then: &declare.Node{Use: "fail"}}, true, declare.ErrBadNode, "node:"},
		{"if without then", declare.Node{Use: "if", Cond: &ok}, true, declare.ErrBadNode, "node:"},
		{"if with of", declare.Node{Use: "if", Cond: &ok, Then: &declare.Node{Use: "fail"}, Of: []declare.Node{ok}}, true, declare.ErrBadNode, "node:"},
		{"bad args", node("gt", declare.Args{"value": "high"}), false, declare.ErrBadArgs, `node: "gt"`},
		{"missing args", node("gt", nil), false, declare.ErrBadArgs, `node: "gt"`},
		{"unknown arg", node("even", declare.Args{"typo": 1}), false, declare.ErrBadArgs, "typo"},
		{"nested path in all", node("all", nil, ok, node("any", nil, ok, node("evn", nil))), false, declare.ErrUnknownName, "node.of[1].of[1]:"},
		{"path in not", node("not", nil, node("evn", nil)), false, declare.ErrUnknownName, "node.of[0]:"},
		{"path in seq", node("seq", nil, node("say", declare.Args{"msg": "a"}), node("nope", nil)), true, declare.ErrUnknownName, "node.of[1]:"},
		{"path in if.cond", declare.Node{Use: "if", Cond: &declare.Node{Use: "evn"}, Then: &declare.Node{Use: "fail"}}, true, declare.ErrUnknownName, "node.cond:"},
		{"path in if.then", declare.Node{Use: "if", Cond: &ok, Then: &declare.Node{Use: "nope"}}, true, declare.ErrUnknownName, "node.then:"},
		{"path in if.else", declare.Node{Use: "if", Cond: &ok, Then: &declare.Node{Use: "fail"}, Else: &declare.Node{Use: "nope"}}, true, declare.ErrUnknownName, "node.else:"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.asAction {
				_, err = cat.Action(tc.n)
			} else {
				_, err = cat.Condition(tc.n)
			}
			if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.path) {
				t.Errorf("err = %v, want %v mentioning %q", err, tc.want, tc.path)
			}
		})
	}
}

func TestSeveralErrorsAreReportedTogether(t *testing.T) {
	cat := testCatalog(t)
	_, err := cat.Condition(node("all", nil, node("a", nil), node("even", nil), node("b", nil)))
	if !errors.Is(err, declare.ErrUnknownName) || !strings.Contains(err.Error(), "node.of[0]") || !strings.Contains(err.Error(), "node.of[2]") {
		t.Errorf("err = %v, want both bad children", err)
	}
}

func TestNamespacesAreSeparate(t *testing.T) {
	cat := testCatalog(t)
	if _, err := cat.Condition(node("shared", nil)); err != nil {
		t.Errorf("condition: %v", err)
	}
	if _, err := cat.Action(node("shared", nil)); err != nil {
		t.Errorf("action: %v", err)
	}
}

func TestCombinatorsFromData(t *testing.T) {
	cat := testCatalog(t)
	gt := func(v int) declare.Node { return node("gt", declare.Args{"value": v}) }
	even := node("even", nil)
	tests := []struct {
		name string
		n    declare.Node
		in   int
		want bool
	}{
		{"leaf", even, 4, true},
		{"all", node("all", nil, even, gt(10)), 12, true},
		{"all fails", node("all", nil, even, gt(10)), 4, false},
		{"all empty", node("all", nil), 1, true},
		{"any", node("any", nil, even, gt(10)), 11, true},
		{"any fails", node("any", nil, even, gt(10)), 3, false},
		{"any empty", node("any", nil), 1, false},
		{"not", node("not", nil, even), 3, true},
		{"nested", node("all", nil, node("not", nil, even), node("any", nil, gt(10), gt(2))), 3, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := cat.Condition(tc.n)
			if err != nil {
				t.Fatal(err)
			}
			if got := c(newCx(tc.in)); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestActionsFromData(t *testing.T) {
	cat := testCatalog(t)
	say := func(m string) declare.Node { return node("say", declare.Args{"msg": m}) }
	even := node("even", nil)
	tests := []struct {
		name string
		n    declare.Node
		in   int
		log  []string
		err  bool
	}{
		{"leaf", say("a"), 1, []string{"a"}, false},
		{"seq in order", node("seq", nil, say("a"), say("b")), 1, []string{"a", "b"}, false},
		{"seq stops at the first error", node("seq", nil, say("a"), node("fail", nil), say("b")), 1, []string{"a"}, true},
		{"seq empty", node("seq", nil), 1, nil, false},
		{"if then", declare.Node{Use: "if", Cond: &even, Then: ptr(say("then")), Else: ptr(say("else"))}, 2, []string{"then"}, false},
		{"if else", declare.Node{Use: "if", Cond: &even, Then: ptr(say("then")), Else: ptr(say("else"))}, 3, []string{"else"}, false},
		{"if without else", declare.Node{Use: "if", Cond: &even, Then: ptr(say("then"))}, 3, nil, false},
		{"if nested in seq", node("seq", nil, declare.Node{Use: "if", Cond: &even, Then: ptr(say("e"))}, say("end")), 2, []string{"e", "end"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := cat.Action(tc.n)
			if err != nil {
				t.Fatal(err)
			}
			c := newCx(tc.in)
			err = a(c)
			if (err != nil) != tc.err || !slices.Equal(*c.log, tc.log) {
				t.Errorf("log %v err %v, want %v err %v", *c.log, err, tc.log, tc.err)
			}
		})
	}
}

func TestListHelpers(t *testing.T) {
	cat := testCatalog(t)
	if c, err := cat.ConditionAll(nil); c != nil || err != nil {
		t.Errorf("empty ConditionAll = %v %v, want nil (always true)", c, err)
	}
	if a, err := cat.ActionSeq(nil); a != nil || err != nil {
		t.Errorf("empty ActionSeq = %v %v, want nil (no-op)", a, err)
	}
	all, err := cat.ConditionAll([]declare.Node{node("even", nil), node("gt", declare.Args{"value": 2})})
	if err != nil || !all(newCx(4)) || all(newCx(2)) || all(newCx(5)) {
		t.Errorf("ConditionAll should AND its nodes (err %v)", err)
	}
	seq, err := cat.ActionSeq([]declare.Node{node("say", declare.Args{"msg": "a"}), node("say", declare.Args{"msg": "b"})})
	c := newCx(0)
	if err != nil || seq(c) != nil || !slices.Equal(*c.log, []string{"a", "b"}) {
		t.Errorf("ActionSeq: %v %v", *c.log, err)
	}
	_, err = cat.ConditionAll([]declare.Node{node("even", nil), node("nope", nil)})
	if !errors.Is(err, declare.ErrUnknownName) || !strings.Contains(err.Error(), "nodes[1]:") {
		t.Errorf("err = %v, want a path into the list", err)
	}
	_, err = cat.ActionSeq([]declare.Node{node("nope", nil)})
	if !errors.Is(err, declare.ErrUnknownName) || !strings.Contains(err.Error(), "nodes[0]:") {
		t.Errorf("err = %v", err)
	}
}

func TestCatalogIsConcurrencySafe(t *testing.T) {
	cat := testCatalog(t)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			cat.RegisterCondition("dyn"+string(rune('a'+g)), func(declare.Args) (logic.Condition[cx], error) { return func(cx) bool { return true }, nil })
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				if _, err := cat.Condition(node("even", nil)); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}

func ptr[T any](v T) *T { return &v }
