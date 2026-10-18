package declare

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Args are the free-form parameters of a named condition or action, as decoded
// from data. JSON numbers arrive as float64 and YAML integers as int; the typed
// getters accept either. Every getter error wraps ErrBadArgs.
type Args map[string]any

// String returns the required string argument key.
func (a Args) String(key string) (string, error) {
	v, ok := a[key]
	if !ok {
		return "", fmt.Errorf("%w: %q is required", ErrBadArgs, key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%w: %q must be a string, got %T", ErrBadArgs, key, v)
	}
	return s, nil
}

// StringOr returns the string argument key, or def if it is absent.
func (a Args) StringOr(key, def string) (string, error) {
	if _, ok := a[key]; !ok {
		return def, nil
	}
	return a.String(key)
}

// Strings returns key as a list of strings. A single string is a list of one;
// an absent key is nil.
func (a Args) Strings(key string) ([]string, error) {
	v, ok := a[key]
	if !ok {
		return nil, nil
	}
	switch x := v.(type) {
	case string:
		return []string{x}, nil
	case []string:
		return slices.Clone(x), nil
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("%w: %q[%d] must be a string, got %T", ErrBadArgs, key, i, e)
			}
			out[i] = s
		}
		return out, nil
	}
	return nil, fmt.Errorf("%w: %q must be a string or a list of strings, got %T", ErrBadArgs, key, v)
}

// Float returns the required numeric argument key. NaN and infinities are rejected.
func (a Args) Float(key string) (float64, error) {
	v, ok := a[key]
	if !ok {
		return 0, fmt.Errorf("%w: %q is required", ErrBadArgs, key)
	}
	f, ok := toFloat(v)
	if !ok {
		return 0, fmt.Errorf("%w: %q must be a finite number, got %v (%T)", ErrBadArgs, key, v, v)
	}
	return f, nil
}

// FloatOr returns the numeric argument key, or def if it is absent.
func (a Args) FloatOr(key string, def float64) (float64, error) {
	if _, ok := a[key]; !ok {
		return def, nil
	}
	return a.Float(key)
}

// Int returns the required integer argument key; a float with no fractional
// part (as JSON produces) is accepted.
func (a Args) Int(key string) (int64, error) {
	v, ok := a[key]
	if !ok {
		return 0, fmt.Errorf("%w: %q is required", ErrBadArgs, key)
	}
	n, ok := toInt(v)
	if !ok {
		return 0, fmt.Errorf("%w: %q must be an integer, got %v (%T)", ErrBadArgs, key, v, v)
	}
	return n, nil
}

// IntOr returns the integer argument key, or def if it is absent.
func (a Args) IntOr(key string, def int64) (int64, error) {
	if _, ok := a[key]; !ok {
		return def, nil
	}
	return a.Int(key)
}

// Bool returns the required boolean argument key.
func (a Args) Bool(key string) (bool, error) {
	v, ok := a[key]
	if !ok {
		return false, fmt.Errorf("%w: %q is required", ErrBadArgs, key)
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%w: %q must be true or false, got %T", ErrBadArgs, key, v)
	}
	return b, nil
}

// BoolOr returns the boolean argument key, or def if it is absent.
func (a Args) BoolOr(key string, def bool) (bool, error) {
	if _, ok := a[key]; !ok {
		return def, nil
	}
	return a.Bool(key)
}

// Unknown returns an error naming every argument not in allowed. Factories
// call it last so that a misspelt key in a data file fails loudly.
func (a Args) Unknown(allowed ...string) error {
	var extra []string
	for k := range a {
		if !slices.Contains(allowed, k) {
			extra = append(extra, k)
		}
	}
	if len(extra) == 0 {
		return nil
	}
	slices.Sort(extra)
	return fmt.Errorf("%w: unknown argument(s) %s", ErrBadArgs, strings.Join(extra, ", "))
}

func toFloat(v any) (float64, bool) {
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case float32:
		f = float64(x)
	case json.Number:
		var err error
		if f, err = x.Float64(); err != nil {
			return 0, false
		}
	default:
		n, ok := toInt(v)
		if !ok {
			return 0, false
		}
		f = float64(n)
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0)
}

func toInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int8:
		return int64(x), true
	case int16:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint8:
		return int64(x), true
	case uint16:
		return int64(x), true
	case uint32:
		return int64(x), true
	case uint:
		return x2i(uint64(x))
	case uint64:
		return x2i(x)
	case float32:
		return f2i(float64(x))
	case float64:
		return f2i(x)
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n, true
		}
		f, err := x.Float64()
		if err != nil {
			return 0, false
		}
		return f2i(f)
	}
	return 0, false
}

func x2i(u uint64) (int64, bool) { return int64(u), u <= math.MaxInt64 }

func f2i(f float64) (int64, bool) {
	if f != math.Trunc(f) || f < -(1<<63) || f >= 1<<63 { // also false for NaN and Inf
		return 0, false
	}
	return int64(f), true
}
