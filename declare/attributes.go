package declare

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/Whealinghold/rumour_mill/logic"
)

// Attributes tells the built-in attribute conditions how to read named
// attributes of the things a condition talks about, inside the caller's context
// type C. Lookup is the caller's only glue.
type Attributes[C any] struct {
	// Sides lists the side names a data file may use (for example the parties
	// of an encounter). Required and non-empty; a side not listed here is
	// rejected when the file loads.
	Sides []string
	// Lookup returns the attribute key of side in ctx, and false if there is
	// none. Required. The key comes from a data file, so treat it as untrusted
	// input: map it through a switch or map, never reflect into arbitrary
	// fields by key. Not safe for concurrent use beyond Lookup itself: the
	// registered conditions hold no state, so they are as concurrency-safe as
	// Lookup is.
	Lookup func(ctx C, side, key string) (any, bool)
}

// AttributeNames are the condition names RegisterAttributeOps registers. An
// empty field takes its default, so the zero value is valid.
type AttributeNames struct {
	Equals string `json:"equals" yaml:"equals"` // default "attr_equals"
	In     string `json:"in" yaml:"in"`         // default "attr_in"
	Same   string `json:"same" yaml:"same"`     // default "attr_same"
}

// DefaultAttributeNames returns attr_equals, attr_in and attr_same.
func DefaultAttributeNames() AttributeNames {
	return AttributeNames{Equals: "attr_equals", In: "attr_in", Same: "attr_same"}
}

func (n AttributeNames) resolved() AttributeNames {
	d := DefaultAttributeNames()
	if n.Equals == "" {
		n.Equals = d.Equals
	}
	if n.In == "" {
		n.In = d.In
	}
	if n.Same == "" {
		n.Same = d.Same
	}
	return n
}

// RegisterAttributeOps registers exactly three conditions, named by n:
//
//	equals  args: side, key, value (any type, required)
//	in      args: side, key, values (non-empty list)
//	same    args: side, key, other_side, other_key (default key)
//
// Each is true when the attribute read through a.Lookup equals the value, is
// equal to one of the values, or equals the other side's attribute. Numbers
// compare by float64 value, so Go ints and loaded JSON numbers agree (integers
// above 2^53 lose precision). A missing attribute makes every one of them
// false, so not over it is true. Values that cannot be compared are never
// equal; nothing panics. Side names are checked when the file loads.
//
// It returns ErrBadBindings if Sides or Lookup is missing, and ErrDuplicateName
// if two resolved names are equal or any is taken or reserved. Either all three
// register or none. Not safe for concurrent use beyond Lookup itself; the
// registered closures hold no mutable state.
func (c *Catalog[C]) RegisterAttributeOps(a Attributes[C], n AttributeNames) error {
	if len(a.Sides) == 0 || a.Lookup == nil {
		return fmt.Errorf("%w: Sides and Lookup are required", ErrBadBindings)
	}
	n = n.resolved()
	if n.Equals == n.In || n.Equals == n.Same || n.In == n.Same {
		return fmt.Errorf("%w: attribute names must differ: %q, %q, %q", ErrDuplicateName, n.Equals, n.In, n.Same)
	}
	sides := slices.Clone(a.Sides)
	look := a.Lookup
	facs := []struct {
		name string
		f    func(Args) (logic.Condition[C], error)
	}{
		{n.Equals, func(args Args) (logic.Condition[C], error) { return attrEquals(args, sides, look) }},
		{n.In, func(args Args) (logic.Condition[C], error) { return attrIn(args, sides, look) }},
		{n.Same, func(args Args) (logic.Condition[C], error) { return attrSame(args, sides, look) }},
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range facs {
		if err := checkName(f.name, c.conds[f.name] != nil); err != nil {
			return err
		}
	}
	for _, f := range facs {
		c.conds[f.name] = f.f
	}
	return nil
}

// sideKey reads and checks the side and key args shared by all three.
func sideKey(a Args, sides []string, sideArg string) (string, error) {
	s, err := a.String(sideArg)
	if err != nil {
		return "", err
	}
	if !slices.Contains(sides, s) {
		return "", fmt.Errorf("%w: %q is %q, want one of %v", ErrBadArgs, sideArg, s, sides)
	}
	return s, nil
}

func attrEquals[C any](a Args, sides []string, look func(C, string, string) (any, bool)) (logic.Condition[C], error) {
	side, err := sideKey(a, sides, "side")
	if err != nil {
		return nil, err
	}
	key, err := a.String("key")
	if err != nil {
		return nil, err
	}
	want, ok := a["value"]
	if !ok {
		return nil, fmt.Errorf("%w: \"value\" is required", ErrBadArgs)
	}
	if err := a.Unknown("side", "key", "value"); err != nil {
		return nil, err
	}
	return func(ctx C) bool {
		got, ok := look(ctx, side, key)
		return ok && attrEqual(got, want)
	}, nil
}

func attrIn[C any](a Args, sides []string, look func(C, string, string) (any, bool)) (logic.Condition[C], error) {
	side, err := sideKey(a, sides, "side")
	if err != nil {
		return nil, err
	}
	key, err := a.String("key")
	if err != nil {
		return nil, err
	}
	raw, ok := a["values"]
	if !ok {
		return nil, fmt.Errorf("%w: \"values\" is required", ErrBadArgs)
	}
	rv := reflect.ValueOf(raw)
	if rv.Kind() != reflect.Slice || rv.Len() == 0 {
		return nil, fmt.Errorf("%w: \"values\" must be a non-empty list, got %T", ErrBadArgs, raw)
	}
	vals := make([]any, rv.Len())
	for i := range vals {
		vals[i] = rv.Index(i).Interface()
	}
	if err := a.Unknown("side", "key", "values"); err != nil {
		return nil, err
	}
	return func(ctx C) bool {
		got, ok := look(ctx, side, key)
		if !ok {
			return false
		}
		return slices.ContainsFunc(vals, func(v any) bool { return attrEqual(got, v) })
	}, nil
}

func attrSame[C any](a Args, sides []string, look func(C, string, string) (any, bool)) (logic.Condition[C], error) {
	side, err := sideKey(a, sides, "side")
	if err != nil {
		return nil, err
	}
	key, err := a.String("key")
	if err != nil {
		return nil, err
	}
	other, err := sideKey(a, sides, "other_side")
	if err != nil {
		return nil, err
	}
	otherKey, err := a.StringOr("other_key", key)
	if err != nil {
		return nil, err
	}
	if err := a.Unknown("side", "key", "other_side", "other_key"); err != nil {
		return nil, err
	}
	return func(ctx C) bool {
		x, ok := look(ctx, side, key)
		if !ok {
			return false
		}
		y, ok := look(ctx, other, otherKey)
		return ok && attrEqual(x, y)
	}, nil
}

// attrEqual compares two attribute values: numbers by float64 value, anything
// else with ==. Uncomparable values are unequal, never a panic.
//
// shortcut: integers above 2^53 lose precision under float64 normalisation;
// upgrade only if a consumer reports it.
func attrEqual(a, b any) (eq bool) {
	if x, ok := toFloat(a); ok {
		if y, ok := toFloat(b); ok {
			return x == y
		}
		return false
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if !reflect.TypeOf(a).Comparable() || !reflect.TypeOf(b).Comparable() {
		return false
	}
	defer func() {
		if recover() != nil { // an interface field holding a slice or map
			eq = false
		}
	}()
	return a == b
}
