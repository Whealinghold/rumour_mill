package logic_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Whealinghold/rumour_mill/logic"
)

type ctx struct{ n int }

func isEven(c ctx) bool  { return c.n%2 == 0 }
func isBig(c ctx) bool   { return c.n >= 10 }
func isThree(c ctx) bool { return c.n == 3 }

func TestConditionCombinators(t *testing.T) {
	var none logic.Condition[ctx]
	tests := []struct {
		name string
		cond logic.Condition[ctx]
		n    int
		want bool
	}{
		{"All empty is true", logic.All[ctx](), 1, true},
		{"All both", logic.All(isEven, isBig), 12, true},
		{"All one fails", logic.All(isEven, isBig), 4, false},
		{"All skips nil", logic.All(none, isEven), 3, false},
		{"Any empty is false", logic.Any[ctx](), 1, false},
		{"Any first", logic.Any(isEven, isBig), 4, true},
		{"Any second", logic.Any(isEven, isBig), 11, true},
		{"Any none", logic.Any(isEven, isBig), 3, false},
		{"Any nil counts as true", logic.Any(none), 3, true},
		{"Not true", logic.Not(isEven), 4, false},
		{"Not false", logic.Not(isEven), 3, true},
		{"Not nil is false", logic.Not(none), 3, false},
		{"nested", logic.All(logic.Not(isEven), logic.Any(isBig, isThree)), 3, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cond(ctx{tc.n}); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConditionsShortCircuit(t *testing.T) {
	calls := 0
	counting := func(result bool) logic.Condition[ctx] {
		return func(ctx) bool { calls++; return result }
	}
	logic.All(counting(false), counting(true))(ctx{})
	logic.Any(counting(true), counting(false))(ctx{})
	if calls != 2 {
		t.Errorf("made %d calls, want 2 (one per short-circuited combinator)", calls)
	}
}

func TestSequence(t *testing.T) {
	boom := errors.New("boom")
	var log []string
	step := func(name string, err error) logic.Action[ctx] {
		return func(ctx) error { log = append(log, name); return err }
	}
	tests := []struct {
		name string
		act  logic.Action[ctx]
		log  []string
		err  error
	}{
		{"empty", logic.Sequence[ctx](), nil, nil},
		{"in order", logic.Sequence(step("a", nil), step("b", nil)), []string{"a", "b"}, nil},
		{"stops at first error", logic.Sequence(step("a", nil), step("b", boom), step("c", nil)), []string{"a", "b"}, boom},
		{"skips nil", logic.Sequence(nil, step("a", nil)), []string{"a"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			log = nil
			err := tc.act(ctx{})
			if !errors.Is(err, tc.err) && !(err == nil && tc.err == nil) {
				t.Errorf("err = %v, want %v", err, tc.err)
			}
			if fmt.Sprint(log) != fmt.Sprint(tc.log) {
				t.Errorf("ran %v, want %v", log, tc.log)
			}
		})
	}
}

func TestIf(t *testing.T) {
	boom := errors.New("boom")
	var ran string
	then := func(ctx) error { ran = "then"; return nil }
	otherwise := func(ctx) error { ran = "else"; return boom }
	tests := []struct {
		name      string
		cond      logic.Condition[ctx]
		then, els logic.Action[ctx]
		n         int
		ran       string
		err       error
	}{
		{"true runs then", isEven, then, otherwise, 2, "then", nil},
		{"false runs else and returns its error", isEven, then, otherwise, 3, "else", boom},
		{"false with nil else is a no-op", isEven, then, nil, 3, "", nil},
		{"true with nil then is a no-op", isEven, nil, otherwise, 2, "", nil},
		{"nil condition is true", nil, then, otherwise, 3, "then", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ran = ""
			err := logic.If(tc.cond, tc.then, tc.els)(ctx{tc.n})
			if ran != tc.ran || !errors.Is(err, tc.err) && !(err == nil && tc.err == nil) {
				t.Errorf("ran %q err %v, want %q %v", ran, err, tc.ran, tc.err)
			}
		})
	}
}

// Conditions and actions are plain functions over a context type you choose.
func Example() {
	type room struct {
		lit      bool
		occupied bool
	}
	lit := func(r *room) bool { return r.lit }
	occupied := func(r *room) bool { return r.occupied }

	dimLights := logic.If(
		logic.All(logic.Not(occupied), lit),
		func(r *room) error { r.lit = false; return nil },
		nil,
	)

	r := &room{lit: true}
	_ = dimLights(r)
	fmt.Println("lit:", r.lit)

	r = &room{lit: true, occupied: true}
	_ = dimLights(r)
	fmt.Println("lit:", r.lit)

	// Output:
	// lit: false
	// lit: true
}
