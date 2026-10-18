// Package logic defines the condition and action function types used by rules
// and spreading filters, and the generic combinators that build bigger ones
// from smaller ones.
//
// C is a caller-defined context type: whatever the conditions and actions need
// to see, typically the entity being processed, the event and the world. The
// package never looks inside C and contains no domain vocabulary.
//
// A nil Condition is treated as always true and a nil Action as a no-op, so
// optional parts can be left unset without nil checks at the call site.
package logic

// Condition reports whether something holds for ctx.
type Condition[C any] func(ctx C) bool

// Action does something to or with ctx and may fail.
type Action[C any] func(ctx C) error

// All is true when every condition is true, checked left to right and
// short-circuiting. All() is true.
func All[C any](cs ...Condition[C]) Condition[C] {
	return func(ctx C) bool {
		for _, c := range cs {
			if c != nil && !c(ctx) {
				return false
			}
		}
		return true
	}
}

// Any is true when at least one condition is true, checked left to right and
// short-circuiting. Any() is false; a nil condition counts as true.
func Any[C any](cs ...Condition[C]) Condition[C] {
	return func(ctx C) bool {
		for _, c := range cs {
			if c == nil || c(ctx) {
				return true
			}
		}
		return false
	}
}

// Not inverts c. Not(nil) is false.
func Not[C any](c Condition[C]) Condition[C] {
	return func(ctx C) bool { return c != nil && !c(ctx) }
}

// Sequence runs the actions in order and stops at, and returns, the first
// error. Sequence() does nothing.
func Sequence[C any](as ...Action[C]) Action[C] {
	return func(ctx C) error {
		for _, a := range as {
			if a == nil {
				continue
			}
			if err := a(ctx); err != nil {
				return err
			}
		}
		return nil
	}
}

// If runs then when cond is true and otherwise when it is false. Either
// branch may be nil.
func If[C any](cond Condition[C], then, otherwise Action[C]) Action[C] {
	return func(ctx C) error {
		branch := otherwise
		if cond == nil || cond(ctx) {
			branch = then
		}
		if branch == nil {
			return nil
		}
		return branch(ctx)
	}
}
