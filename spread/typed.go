package spread

import (
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/logic"
)

// TypedEncounter is Encounter with the caller's own holder type H in place of
// the Holder interface. Fact and Now mean what they mean on Encounter.
type TypedEncounter[H Holder] struct {
	Teller, Listener H
	Fact             knowledge.Fact
	Now              knowledge.Tick
}

// TypedOption configures Typed and TypedTransform.
type TypedOption func(*typedOpts)

type typedOpts struct{ onMismatch func(Encounter) }

// OnMismatch calls f whenever the teller or listener is not the expected holder
// type, before the encounter is blocked (Typed) or passed through unchanged
// (TypedTransform). A mismatch is a wiring bug that otherwise looks like a
// policy decision, so use it to count or log. It may be given several times; each runs in order. f must not change the outcome.
func OnMismatch(f func(Encounter)) TypedOption {
	return func(o *typedOpts) {
		if f == nil {
			return
		}
		prev := o.onMismatch
		o.onMismatch = func(e Encounter) {
			if prev != nil {
				prev(e)
			}
			f(e)
		}
	}
}

func newTypedOpts(opts []TypedOption) typedOpts {
	var o typedOpts
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

func (o typedOpts) mismatch(e Encounter) {
	if e.mismatches != nil {
		*e.mismatches++
	}
	if o.onMismatch != nil {
		o.onMismatch(e)
	}
}

// Typed adapts f, a filter written in terms of the caller's holder type H (a
// pointer or interface type), into a filter for WithFilter, so the filter body
// needs no type assertion.
//
// Fail closed: if the teller or the listener is not an H (including a nil
// Holder), the encounter is blocked and f is not called. A nil f returns a nil
// condition, which WithFilter ignores. Typed holds no state; concurrency is
// that of f.
func Typed[H Holder](f func(TypedEncounter[H]) bool, opts ...TypedOption) logic.Condition[Encounter] {
	if f == nil {
		return nil
	}
	o := newTypedOpts(opts)
	return func(e Encounter) bool {
		t, ok := e.Teller.(H)
		if !ok {
			o.mismatch(e)
			return false
		}
		l, ok := e.Listener.(H)
		if !ok {
			o.mismatch(e)
			return false
		}
		return f(TypedEncounter[H]{Teller: t, Listener: l, Fact: e.Fact, Now: e.Now})
	}
}

// TypedTransform adapts f, a transform written in terms of the caller's holder
// type H, into a function assignable to Spreader.Transform. TypedEncounter.Fact
// is the teller's fact before decay, as Encounter.Fact is; delivered is the
// copy about to be delivered.
//
// If the teller or the listener is not an H (including a nil Holder), f is not
// called and delivered is returned unchanged. A nil f returns nil, which leaves
// Spreader.Transform unset. What f returns is still subject to the fixed-field
// restore described on Transform. TypedTransform holds no state; concurrency is
// that of f.
func TypedTransform[H Holder](f func(TypedEncounter[H], knowledge.Fact) knowledge.Fact, opts ...TypedOption) func(Encounter, knowledge.Fact) knowledge.Fact {
	if f == nil {
		return nil
	}
	o := newTypedOpts(opts)
	return func(e Encounter, delivered knowledge.Fact) knowledge.Fact {
		t, ok := e.Teller.(H)
		if !ok {
			o.mismatch(e)
			return delivered
		}
		l, ok := e.Listener.(H)
		if !ok {
			o.mismatch(e)
			return delivered
		}
		return f(TypedEncounter[H]{Teller: t, Listener: l, Fact: e.Fact, Now: e.Now}, delivered)
	}
}
