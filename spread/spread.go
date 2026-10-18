// Package spread moves facts between holders the way gossip does: a teller
// offers what it knows to listeners, confidence decays on every hop, and a
// caller-supplied policy decides who may tell whom what.
//
// Nothing here knows about rooms, groups or relationships. The caller chooses
// the listeners and supplies the filters; the package contains no vocabulary
// of any setting and uses no randomness, so spreading is fully deterministic.
package spread

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/logic"
)

// ErrInvalidConfig is wrapped by every Config.Validate failure.
var ErrInvalidConfig = errors.New("spread: invalid config")

// Config holds the tunables of a Spreader. Use DefaultConfig and override
// fields. The zero Config is not valid (a zero DecayFactor would erase every
// fact), so start from DefaultConfig.
type Config struct {
	// DecayFactor multiplies confidence on each hop, in (0,1]; 1 means no loss.
	DecayFactor float64 `json:"decay_factor" yaml:"decay_factor"`
	// MinConfidence is the lowest confidence a delivered copy may have.
	MinConfidence float64 `json:"min_confidence" yaml:"min_confidence"`
	// MinSourceConfidence is the lowest confidence at which a teller offers a fact.
	MinSourceConfidence float64 `json:"min_source_confidence" yaml:"min_source_confidence"`
	// MaxHops is the most hops a delivered copy may have travelled; 0 = unlimited.
	MaxHops int `json:"max_hops" yaml:"max_hops"`
	// ReceivedKind is the source kind stamped on delivered copies. It should be a
	// kind the listeners' stores rank.
	ReceivedKind string `json:"received_kind" yaml:"received_kind"`
	// SkipBackToSource stops a listener being told a fact that came from them.
	SkipBackToSource bool `json:"skip_back_to_source" yaml:"skip_back_to_source"`
}

// DefaultConfig returns the documented defaults.
func DefaultConfig() Config {
	return Config{
		DecayFactor:         0.8,
		MinConfidence:       0.2,
		MinSourceConfidence: 0.3,
		MaxHops:             0,
		ReceivedKind:        "relayed",
		SkipBackToSource:    true,
	}
}

// Validate reports every problem with c, joined; each wraps ErrInvalidConfig.
func (c Config) Validate() error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: "+format, append([]any{ErrInvalidConfig}, args...)...))
	}
	if !(c.DecayFactor > 0 && c.DecayFactor <= 1) {
		bad("decay_factor %v outside (0,1]", c.DecayFactor)
	}
	if !(c.MinConfidence >= 0 && c.MinConfidence <= 1) {
		bad("min_confidence %v outside [0,1]", c.MinConfidence)
	}
	if !(c.MinSourceConfidence >= 0 && c.MinSourceConfidence <= 1) {
		bad("min_source_confidence %v outside [0,1]", c.MinSourceConfidence)
	}
	if c.MaxHops < 0 {
		bad("max_hops %d is negative", c.MaxHops)
	}
	if c.ReceivedKind == "" {
		bad("received_kind is empty")
	}
	return errors.Join(errs...)
}

// Holder is a participant in a spread: anything with a stable ID and a
// knowledge store.
type Holder interface {
	ID() string
	Knowledge() *knowledge.Store // nil: holds nothing and cannot receive
}

// Encounter is what a filter judges: one fact, one teller, one listener. Fact
// is the teller's version, before decay, and must be treated as read-only. A
// filter that needs the caller's concrete types type-asserts Teller and Listener. Keep that
// assertion in one small helper next to the filter, as the ward example in the
// declare package does (e.Listener.(*staff).role), so the rest of the policy
// stays in terms of your own types.
type Encounter struct {
	Teller, Listener Holder
	Fact             knowledge.Fact
	Now              knowledge.Tick
}

// Transfer records one delivery attempt.
type Transfer struct {
	FactID     string
	To         string            // listener ID
	Confidence float64           // as delivered
	Outcome    knowledge.Outcome // what the listener's store did with it
}

// Result lists the delivery attempts of one Spread call, in order. A Transfer is
// recorded for every copy handed to a listener's store, including ones the
// store rejected because the listener already knew better; check Outcome.
type Result struct{ Transfers []Transfer }

// Spreader carries facts from tellers to listeners.
//
// A Spreader holds no mutable state, but it reads and writes knowledge stores,
// which are not safe for concurrent use: the caller serialises access to the
// stores involved. Create one with New.
type Spreader struct {
	// Transform, if non-nil, receives the copy about to be delivered (decayed
	// confidence, new source and provenance already applied) and returns what is
	// actually delivered. Only its Confidence, Payload and Expiry are used; ID,
	// Type, Subject, Discriminator, Source, Prov, Observed and Private are
	// restored, so a transform cannot change what a fact is or forge where it
	// came from. A NaN confidence drops the delivery. Set it after New and
	// before the first Spread.
	Transform func(knowledge.Fact) knowledge.Fact

	cfg     Config
	filters []logic.Condition[Encounter]
}

// Option configures a Spreader at construction.
type Option func(*Spreader)

// WithFilter adds a filter. It may be given several times; every filter must
// return true for a fact to be told. A nil filter is ignored.
func WithFilter(c logic.Condition[Encounter]) Option {
	return func(s *Spreader) {
		if c != nil {
			s.filters = append(s.filters, c)
		}
	}
}

// New returns a Spreader, or an error wrapping ErrInvalidConfig.
func New(cfg Config, opts ...Option) (*Spreader, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	s := &Spreader{cfg: cfg}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Spread offers the teller's facts to each listener, in the order given.
//
// A fact is offered when it is unexpired, not Private, at or above
// MinSourceConfidence, and has travelled fewer than MaxHops hops (when set).
// Offers go highest confidence first, then by ascending ID. For each
// listener (skipping nil ones, the teller itself, and holders without a
// store) a fact is skipped if SkipBackToSource applies or any filter says no.
// Otherwise the listener's store receives a copy with confidence multiplied by
// DecayFactor, the configured ReceivedKind, Observed set to now, Prov.From the
// teller, Prov.Hops one higher, and Private cleared; the copy is dropped if its
// confidence is below MinConfidence. The store's normal merge rules decide
// whether the copy is kept. The teller's store is never modified.
//
// Rumours cannot loop forever: a relayed copy never outranks the original, and
// confidence only falls, so in a ring of holders the facts settle and stop
// changing.
//
// Store errors are joined into the returned error; deliveries already made
// stay (Spread is not transactional).
func (s *Spreader) Spread(teller Holder, listeners []Holder, now knowledge.Tick) (Result, error) {
	var res Result
	if teller == nil || teller.Knowledge() == nil {
		return res, nil
	}
	floor := s.cfg.MinSourceConfidence
	var offers []knowledge.Fact
	for _, f := range teller.Knowledge().Find(knowledge.Query{MinConfidence: &floor}, now) {
		if f.Private || (s.cfg.MaxHops > 0 && f.Prov.Hops >= s.cfg.MaxHops) {
			continue
		}
		offers = append(offers, f)
	}
	slices.SortStableFunc(offers, func(a, b knowledge.Fact) int {
		return cmp.Or(cmp.Compare(b.Confidence, a.Confidence), cmp.Compare(a.ID, b.ID))
	})

	var errs []error
	for _, l := range listeners {
		if l == nil || l.ID() == teller.ID() || l.Knowledge() == nil {
			continue
		}
		for _, fact := range offers {
			if s.cfg.SkipBackToSource && fact.Prov.From != "" && fact.Prov.From == l.ID() {
				continue
			}
			if !s.allowed(Encounter{Teller: teller, Listener: l, Fact: fact, Now: now}) {
				continue
			}
			out, ok := s.delivered(teller, fact, now)
			if !ok {
				continue
			}
			add, err := l.Knowledge().Add(out)
			if err != nil {
				errs = append(errs, fmt.Errorf("spread: %s -> %s: %w", teller.ID(), l.ID(), err))
				continue
			}
			res.Transfers = append(res.Transfers, Transfer{FactID: fact.ID, To: l.ID(), Confidence: out.Confidence, Outcome: add.Outcome})
		}
	}
	return res, errors.Join(errs...)
}

func (s *Spreader) allowed(e Encounter) bool {
	for _, f := range s.filters {
		if !f(e) {
			return false
		}
	}
	return true
}

// delivered builds the copy the listener receives; ok is false if it falls
// below MinConfidence.
func (s *Spreader) delivered(teller Holder, fact knowledge.Fact, now knowledge.Tick) (knowledge.Fact, bool) {
	out := fact
	out.Confidence = fact.Confidence * s.cfg.DecayFactor
	out.Source = s.cfg.ReceivedKind
	out.Observed = now
	out.Prov = knowledge.Provenance{From: teller.ID(), Hops: fact.Prov.Hops + 1, EventID: fact.Prov.EventID}
	out.Private = false
	if out.Expiry != nil {
		e := *out.Expiry
		out.Expiry = &e
	}
	if s.Transform != nil {
		t := s.Transform(out)
		out.Confidence, out.Payload, out.Expiry = t.Confidence, t.Payload, t.Expiry
	}
	if !(out.Confidence >= s.cfg.MinConfidence) { // also false for NaN
		return knowledge.Fact{}, false
	}
	out.Confidence = min(out.Confidence, 1)
	return out, true
}
