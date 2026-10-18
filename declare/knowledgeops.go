package declare

import (
	"fmt"

	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/logic"
)

// Bindings tell the built-in knowledge operations how to reach what they need
// inside the caller's context type C.
type Bindings[C any] struct {
	// Store returns the knowledge store of the entity being processed. If it
	// returns nil, knows is false and add_fact fails with ErrNoStore. Required.
	Store func(C) *knowledge.Store
	// Now returns the current tick. Required.
	Now func(C) knowledge.Tick
	// Subject returns the subject add_fact records facts about, and the one
	// about_subject compares with (often the event's actor). Required.
	Subject func(C) string
	// EventID returns the ID of the causing event. Optional: when set it is
	// recorded as provenance, and per_event uses it to keep occurrences apart.
	EventID func(C) string
}

// RegisterKnowledgeOps registers two names, and only these two:
//
//	knows     (condition) args: type (required); about_subject (bool, default
//	          false); min_confidence (0..1, default the store's query floor);
//	          source (string or list, default any). True if the store holds a
//	          matching unexpired fact. With about_subject the fact's subject must
//	          equal Bindings.Subject.
//	add_fact  (action) args: type, confidence (0..1) and source (required);
//	          ttl (ticks, default none); private (default false); per_event
//	          (default false). Records a fact about Bindings.Subject observed now.
//	          With per_event the discriminator is the event ID, so each occurrence
//	          is a separate fact; otherwise repeats merge under the store's policy.
//
// Everything else a rule tests or does is registered by the caller. It returns
// ErrBadBindings if a required binding is nil, or the errors of
// RegisterCondition/RegisterAction.
func (c *Catalog[C]) RegisterKnowledgeOps(b Bindings[C]) error {
	if b.Store == nil || b.Now == nil || b.Subject == nil {
		return fmt.Errorf("%w: Store, Now and Subject are required", ErrBadBindings)
	}
	if err := c.RegisterCondition("knows", func(a Args) (logic.Condition[C], error) { return knows(a, b) }); err != nil {
		return err
	}
	return c.RegisterAction("add_fact", func(a Args) (logic.Action[C], error) { return addFact(a, b) })
}

func knows[C any](a Args, b Bindings[C]) (logic.Condition[C], error) {
	typ, err := a.String("type")
	if err != nil {
		return nil, err
	}
	about, err := a.BoolOr("about_subject", false)
	if err != nil {
		return nil, err
	}
	sources, err := a.Strings("source")
	if err != nil {
		return nil, err
	}
	q := knowledge.Query{Type: typ, Sources: sources}
	if _, ok := a["min_confidence"]; ok {
		m, err := a.Float("min_confidence")
		if err != nil {
			return nil, err
		}
		if m < 0 || m > 1 {
			return nil, fmt.Errorf("%w: min_confidence %v outside [0,1]", ErrBadArgs, m)
		}
		q.MinConfidence = &m
	}
	if err := a.Unknown("type", "about_subject", "min_confidence", "source"); err != nil {
		return nil, err
	}
	return func(ctx C) bool {
		store := b.Store(ctx)
		if store == nil {
			return false
		}
		subject := b.Subject(ctx)
		for _, f := range store.Find(q, b.Now(ctx)) {
			if !about || f.Subject == subject { // Query.Subject cannot select an empty subject, so compare here
				return true
			}
		}
		return false
	}, nil
}

func addFact[C any](a Args, b Bindings[C]) (logic.Action[C], error) {
	typ, err := a.String("type")
	if err != nil {
		return nil, err
	}
	source, err := a.String("source")
	if err != nil {
		return nil, err
	}
	confidence, err := a.Float("confidence")
	if err != nil {
		return nil, err
	}
	if confidence < 0 || confidence > 1 {
		return nil, fmt.Errorf("%w: confidence %v outside [0,1]", ErrBadArgs, confidence)
	}
	ttl, err := a.IntOr("ttl", 0)
	if err != nil {
		return nil, err
	}
	if ttl < 0 {
		return nil, fmt.Errorf("%w: ttl %d is negative", ErrBadArgs, ttl)
	}
	private, err := a.BoolOr("private", false)
	if err != nil {
		return nil, err
	}
	perEvent, err := a.BoolOr("per_event", false)
	if err != nil {
		return nil, err
	}
	if perEvent && b.EventID == nil {
		return nil, fmt.Errorf("%w: per_event needs Bindings.EventID", ErrBadArgs)
	}
	if err := a.Unknown("type", "source", "confidence", "ttl", "private", "per_event"); err != nil {
		return nil, err
	}
	return func(ctx C) error {
		store := b.Store(ctx)
		if store == nil {
			return ErrNoStore
		}
		now := b.Now(ctx)
		f := knowledge.Fact{Type: typ, Subject: b.Subject(ctx), Confidence: confidence, Source: source, Observed: now, Private: private}
		if ttl > 0 {
			expiry := now + knowledge.Tick(ttl)
			f.Expiry = &expiry
		}
		if b.EventID != nil {
			f.Prov.EventID = b.EventID(ctx)
			if perEvent {
				f.Discriminator = f.Prov.EventID
			}
		}
		_, err := store.Add(f)
		return err
	}, nil
}
