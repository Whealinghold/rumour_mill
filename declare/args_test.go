package declare_test

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/Whealinghold/rumour_mill/declare"
)

func TestArgsGetters(t *testing.T) {
	a := declare.Args{
		"s": "text", "n": 3, "f": 2.5, "whole": 4.0, "big": uint64(math.MaxUint64), "num": json.Number("7"),
		"numf": json.Number("7.0"), "bad": json.Number("x"), "b": true, "list": []any{"x", "y"}, "strs": []string{"p"},
		"mixed": []any{"x", 2}, "nan": math.NaN(), "inf": math.Inf(1),
	}
	type result struct {
		v   any
		bad bool
	}
	tests := []struct {
		name string
		got  func() (any, error)
		want any
		bad  bool
	}{
		{"String ok", func() (any, error) { return a.String("s") }, "text", false},
		{"String missing", func() (any, error) { return a.String("nope") }, "", true},
		{"String wrong type", func() (any, error) { return a.String("n") }, "", true},
		{"StringOr default", func() (any, error) { return a.StringOr("nope", "d") }, "d", false},
		{"StringOr present", func() (any, error) { return a.StringOr("s", "d") }, "text", false},
		{"StringOr wrong type", func() (any, error) { return a.StringOr("n", "d") }, "", true},
		{"Strings single", func() (any, error) { return a.Strings("s") }, []string{"text"}, false},
		{"Strings any list", func() (any, error) { return a.Strings("list") }, []string{"x", "y"}, false},
		{"Strings string slice", func() (any, error) { return a.Strings("strs") }, []string{"p"}, false},
		{"Strings absent", func() (any, error) { return a.Strings("nope") }, []string(nil), false},
		{"Strings mixed", func() (any, error) { return a.Strings("mixed") }, []string(nil), true},
		{"Strings number", func() (any, error) { return a.Strings("n") }, []string(nil), true},
		{"Float from float", func() (any, error) { return a.Float("f") }, 2.5, false},
		{"Float from int", func() (any, error) { return a.Float("n") }, 3.0, false},
		{"Float from json.Number", func() (any, error) { return a.Float("num") }, 7.0, false},
		{"Float bad json.Number", func() (any, error) { return a.Float("bad") }, 0.0, true},
		{"Float from string", func() (any, error) { return a.Float("s") }, 0.0, true},
		{"Float NaN", func() (any, error) { return a.Float("nan") }, 0.0, true},
		{"Float Inf", func() (any, error) { return a.Float("inf") }, 0.0, true},
		{"Float missing", func() (any, error) { return a.Float("nope") }, 0.0, true},
		{"FloatOr default", func() (any, error) { return a.FloatOr("nope", 1.5) }, 1.5, false},
		{"FloatOr wrong type", func() (any, error) { return a.FloatOr("s", 1.5) }, 0.0, true},
		{"Int from int", func() (any, error) { return a.Int("n") }, int64(3), false},
		{"Int from whole float", func() (any, error) { return a.Int("whole") }, int64(4), false},
		{"Int from fractional float", func() (any, error) { return a.Int("f") }, int64(0), true},
		{"Int from json.Number", func() (any, error) { return a.Int("num") }, int64(7), false},
		{"Int from json.Number 7.0", func() (any, error) { return a.Int("numf") }, int64(7), false},
		{"Int overflow", func() (any, error) { return a.Int("big") }, int64(0), true},
		{"Int from string", func() (any, error) { return a.Int("s") }, int64(0), true},
		{"Int missing", func() (any, error) { return a.Int("nope") }, int64(0), true},
		{"IntOr default", func() (any, error) { return a.IntOr("nope", 9) }, int64(9), false},
		{"IntOr wrong type", func() (any, error) { return a.IntOr("s", 9) }, int64(0), true},
		{"Bool ok", func() (any, error) { return a.Bool("b") }, true, false},
		{"Bool wrong type", func() (any, error) { return a.Bool("s") }, false, true},
		{"Bool missing", func() (any, error) { return a.Bool("nope") }, false, true},
		{"BoolOr default", func() (any, error) { return a.BoolOr("nope", true) }, true, false},
		{"BoolOr wrong type", func() (any, error) { return a.BoolOr("s", true) }, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.got()
			if (err != nil) != tc.bad || (tc.bad && !errors.Is(err, declare.ErrBadArgs)) {
				t.Fatalf("err = %v, want ErrBadArgs: %v", err, tc.bad)
			}
			if !tc.bad && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestArgsUnknown(t *testing.T) {
	a := declare.Args{"type": "x", "typo": 1, "also": 2}
	if err := a.Unknown("type", "typo", "also"); err != nil {
		t.Errorf("all allowed: %v", err)
	}
	err := a.Unknown("type")
	if !errors.Is(err, declare.ErrBadArgs) || err.Error() != "declare: bad arguments: unknown argument(s) also, typo" {
		t.Errorf("err = %v; want both names, sorted", err)
	}
	if err := (declare.Args(nil)).Unknown(); err != nil {
		t.Errorf("nil Args: %v", err)
	}
}

func TestArgsFromRealJSON(t *testing.T) {
	var n declare.Node
	if err := json.Unmarshal([]byte(`{"use":"x","args":{"ttl":2000,"confidence":0.5,"types":["a","b"]}}`), &n); err != nil {
		t.Fatal(err)
	}
	ttl, err1 := n.Args.Int("ttl")
	c, err2 := n.Args.Float("confidence")
	types, err3 := n.Args.Strings("types")
	if err1 != nil || err2 != nil || err3 != nil || ttl != 2000 || c != 0.5 || !reflect.DeepEqual(types, []string{"a", "b"}) {
		t.Errorf("got %v %v %v (%v %v %v)", ttl, c, types, err1, err2, err3)
	}
}
