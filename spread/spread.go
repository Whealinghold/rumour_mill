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
	"log/slog"
	"math"
	"slices"
	"time"

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
// filter that needs the caller's concrete types wraps itself in Typed, which does
// the type assertion once, so the rest of the policy stays in terms of your own
// types.
type Encounter struct {
	Teller, Listener Holder
	Fact             knowledge.Fact
	Now              knowledge.Tick

	// mismatches points at the per-Spread holder-type mismatch counter; nil when
	// the Encounter was not built by a Spreader.
	mismatches *int
}

// Transfer records one delivery attempt.
type Transfer struct {
	FactID     string
	To         string            // listener ID
	Confidence float64           // as delivered
	Outcome    knowledge.Outcome // what the listener's store did with it
	Hops       int               // Prov.Hops of the delivered copy
}

// Dropped counts offers that did not become a delivery, by reason.
type Dropped struct {
	Filtered           int // a filter said no
	BackToSource       int // SkipBackToSource: the fact came from this listener
	BelowMinConfidence int // decayed (or transformed) confidence under MinConfidence
	TransformDropped   int // Transform returned a NaN confidence
}

// Each calls f once per drop reason with its count, in a fixed order, zero counts
// included, so an adapter labels every reason without listing the fields and a
// reason added later is not missed. The reasons are "filtered", "back_to_source",
// "below_min_confidence" and "transform_dropped".
func (d Dropped) Each(f func(reason string, n int)) {
	f("filtered", d.Filtered)
	f("back_to_source", d.BackToSource)
	f("below_min_confidence", d.BelowMinConfidence)
	f("transform_dropped", d.TransformDropped)
}

// Result lists the delivery attempts of one Spread call, in order. A Transfer is
// recorded for every copy handed to a listener's store, including ones the
// store rejected because the listener already knew better; check Outcome.
// Offered counts every (fact, listener) pair considered, so
// Offered == len(Transfers) + Dropped total + store errors (unchanged by
// Withheld: facts never offered at all are counted there, not here). Dropped explains
// why a rumour did not arrive; without it a filter, decay and a faulty
// Transform all look the same.
type Result struct {
	Transfers []Transfer
	Offered   int
	Dropped   Dropped
	// HolderMismatches counts how often a Typed filter or TypedTransform met a
	// teller or listener that was not the expected type during this call. A
	// mismatched Typed filter is also counted in Dropped.Filtered; a mismatched
	// TypedTransform passes the delivery through unchanged.
	HolderMismatches int
	// Withheld (a plain value, safe to retain across goroutines) counts the teller's facts that were not offered to anyone.
	Withheld Withheld
	// SkippedListeners counts listeners that were nil or had no store.
	SkippedListeners SkippedListeners
}

// Withheld counts the teller's facts (not fact-listener pairs) that were not
// offered to anyone, by reason. They are not part of Offered or Dropped. Each
// fact is counted under the first reason that applies, in the order of the
// fields below.
type Withheld struct {
	Expired               int // the fact's Expiry has passed
	BelowSourceConfidence int // confidence under Config.MinSourceConfidence
	Private               int // the fact is Private
	MaxHops               int // the fact has reached Config.MaxHops
}

// Each calls f for "private", "max_hops", "below_source_confidence" and
// "expired" with their counts, zero counts included, so an adapter can seed and
// label every reason without listing fields.
func (w Withheld) Each(f func(reason string, n int)) {
	f("private", w.Private)
	f("max_hops", w.MaxHops)
	f("below_source_confidence", w.BelowSourceConfidence)
	f("expired", w.Expired)
}

// SkippedListeners (a plain value, safe to retain across goroutines) counts listeners passed to Spread that could not be told
// anything, by reason. A listener that is the teller is skipped silently and is
// not counted.
type SkippedListeners struct {
	Nil     int // the listener was nil
	NoStore int // the listener's Knowledge() was nil
}

// Each calls f for "nil_listener" then "no_store" with their counts, zero
// counts included.
func (k SkippedListeners) Each(f func(reason string, n int)) {
	f("nil_listener", k.Nil)
	f("no_store", k.NoStore)
}

// Transform distorts a fact in transit. It receives the same Encounter the
// filters judged (teller, listener, the teller's fact before decay, current
// tick) and the copy about to be delivered (decayed confidence, new source and
// provenance already applied), and returns what is actually delivered. It runs
// after the copy is built and before the MinConfidence check. Only Confidence,
// Payload and Expiry of the result are used; every other field is restored, so
// a transform cannot change what a fact is or forge its origin. A NaN
// confidence drops the delivery; confidence above 1 is clamped.
//
// Treat the Encounter, including Encounter.Fact.Prov.Premises, as read-only:
// the premises slice is shared across the deliveries of one Spread call, and
// this is documented, not enforced. Age is e.Now - e.Fact.Observed. A panic
// propagates. Transform is called synchronously on the caller's goroutine and is
// not safe for concurrent use against a shared store: Spread is not (spec A6),
// so the caller serialises Spread calls and any state the transform touches.
type Transform func(e Encounter, delivered knowledge.Fact) knowledge.Fact

// Spreader carries facts from tellers to listeners.
//
// A Spreader holds no mutable state, but it reads and writes knowledge stores,
// which are not safe for concurrent use: the caller serialises access to the
// stores involved. Create one with New.
type Spreader struct {
	// Transform, if non-nil, is applied to every delivery that passed the
	// filters; see the Transform type. Nil leaves the delivered copy as built.
	Transform Transform

	cfg     Config
	filters []logic.Condition[Encounter]
	hooks   Hooks
	log     *slog.Logger
	clock   func() time.Time // nil: no timing
}

// Hooks are optional observers of a Spreader. Any field may be nil. Hooks are
// called synchronously on the caller's goroutine, must treat their argument as
// read-only, and must not change what Spread does. A Hooks value is
// plain data; the funcs must be safe for concurrent use if the Spreader is shared.
type Hooks struct {
	// OnSpread is called once at the end of every Spread call that has a
	// teller with a store, with its Result and the joined store errors (nil
	// when there were none). A nil teller or one without a store returns early,
	// calls OnSkip instead, and does not call this.
	OnSpread func(Result, error)
	// OnSkip is called instead of OnSpread when Spread returns early, with
	// SkipNilTeller or SkipNoStore.
	OnSkip func(reason string)
	// OnSpreadDone is called after OnSpread with the call's duration (never
	// negative). It only fires when a clock was given with WithClock.
	OnSpreadDone func(time.Duration)
}

// Reasons passed to Hooks.OnSkip.
const (
	SkipNilTeller = "nil_teller" // the teller was nil
	SkipNoStore   = "no_store"   // the teller has no store
)

// SkipReasons returns a fresh slice of every OnSkip reason, so an adapter can
// seed its series. Safe for concurrent use.
func SkipReasons() []string { return []string{SkipNilTeller, SkipNoStore} }

// WithHooks adds observer hooks. It may be given several times (for example one
// for metrics and one for an audit trail); each non-nil field of each Hooks
// runs, in the order given, independently of the others.
func WithHooks(h Hooks) Option {
	return func(s *Spreader) {
		p := s.hooks
		if h.OnSpread != nil {
			s.hooks.OnSpread = func(r Result, err error) {
				if p.OnSpread != nil {
					p.OnSpread(r, err)
				}
				h.OnSpread(r, err)
			}
		}
		if h.OnSkip != nil {
			s.hooks.OnSkip = func(reason string) {
				if p.OnSkip != nil {
					p.OnSkip(reason)
				}
				h.OnSkip(reason)
			}
		}
		if h.OnSpreadDone != nil {
			s.hooks.OnSpreadDone = func(d time.Duration) {
				if p.OnSpreadDone != nil {
					p.OnSpreadDone(d)
				}
				h.OnSpreadDone(d)
			}
		}
	}
}

// WithClock injects the clock used to time Spread for Hooks.OnSpreadDone. A nil
// clock is ignored; without one the clock is never read and OnSpreadDone never
// fires. The clock is called on the caller's goroutine.
func WithClock(now func() time.Time) Option {
	return func(s *Spreader) {
		if now != nil {
			s.clock = now
		}
	}
}

// WithLogger sets the logger. Spread logs one Debug line per call. A nil logger
// is ignored; the default discards everything.
func WithLogger(l *slog.Logger) Option {
	return func(s *Spreader) {
		if l != nil {
			s.log = l
		}
	}
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
	s := &Spreader{cfg: cfg, log: slog.New(slog.DiscardHandler)}
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
// store; the nil and storeless ones are counted in Result.SkippedListeners) a fact is skipped if SkipBackToSource applies or any filter says no.
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
	// Early returns call OnSkip, not OnSpread: there is no Result to report.
	if teller == nil || teller.Knowledge() == nil {
		reason := SkipNoStore
		if teller == nil {
			reason = SkipNilTeller
		}
		s.log.Warn("spread: nil teller or teller without a store; nothing spread")
		if s.hooks.OnSkip != nil {
			s.hooks.OnSkip(reason)
		}
		return res, nil
	}
	var start time.Time
	if s.clock != nil {
		start = s.clock()
	}
	var offers []knowledge.Fact
	for _, f := range teller.Knowledge().All() {
		switch {
		case f.Expiry != nil && *f.Expiry <= now:
			res.Withheld.Expired++
		case f.Confidence < s.cfg.MinSourceConfidence:
			res.Withheld.BelowSourceConfidence++
		case f.Private:
			res.Withheld.Private++
		case s.cfg.MaxHops > 0 && f.Prov.Hops >= s.cfg.MaxHops:
			res.Withheld.MaxHops++
		default:
			offers = append(offers, f)
		}
	}
	slices.SortStableFunc(offers, func(a, b knowledge.Fact) int {
		return cmp.Or(cmp.Compare(b.Confidence, a.Confidence), cmp.Compare(a.ID, b.ID))
	})

	var errs []error
	var mismatches int
	for _, l := range listeners {
		if l == nil {
			res.SkippedListeners.Nil++
			continue
		}
		if l.ID() == teller.ID() {
			continue
		}
		if l.Knowledge() == nil {
			res.SkippedListeners.NoStore++
			continue
		}
		for _, fact := range offers {
			res.Offered++
			if s.cfg.SkipBackToSource && fact.Prov.From != "" && fact.Prov.From == l.ID() {
				res.Dropped.BackToSource++
				continue
			}
			e := Encounter{Teller: teller, Listener: l, Fact: fact, Now: now, mismatches: &mismatches}
			if !s.allowed(e) {
				res.Dropped.Filtered++
				continue
			}
			out, nan, ok := s.delivered(e)
			if !ok {
				if nan {
					res.Dropped.TransformDropped++
				} else {
					res.Dropped.BelowMinConfidence++
				}
				continue
			}
			add, err := l.Knowledge().Add(out)
			if err != nil {
				errs = append(errs, fmt.Errorf("spread: %s -> %s: %w", teller.ID(), l.ID(), err))
				continue
			}
			res.Transfers = append(res.Transfers, Transfer{FactID: fact.ID, To: l.ID(), Confidence: out.Confidence, Outcome: add.Outcome, Hops: out.Prov.Hops})
		}
	}
	res.HolderMismatches = mismatches
	err := errors.Join(errs...)
	var end time.Time
	if s.clock != nil {
		end = s.clock()
	}
	if s.hooks.OnSpread != nil {
		s.hooks.OnSpread(res, err)
	}
	if s.clock != nil && s.hooks.OnSpreadDone != nil {
		s.hooks.OnSpreadDone(max(end.Sub(start), 0))
	}
	if sk := res.SkippedListeners; sk.Nil+sk.NoStore > 0 {
		s.log.Warn("spread: listeners skipped", "teller", teller.ID(), "nil", sk.Nil, "no_store", sk.NoStore)
	}
	if len(errs) > 0 {
		s.log.Warn("spread: store errors", "teller", teller.ID(), "errors", len(errs))
	}
	if res.Dropped.TransformDropped > 0 {
		s.log.Warn("spread: transform returned NaN confidence", "teller", teller.ID(), "dropped", res.Dropped.TransformDropped)
	}
	if mismatches > 0 {
		s.log.Warn("spread: holder type mismatch", "teller", teller.ID(), "mismatches", mismatches)
	}
	s.log.Debug("spread", "teller", teller.ID(), "offered", res.Offered, "delivered", len(res.Transfers), "dropped", res.Dropped, "errors", len(errs), "withheld", res.Withheld, "skipped_listeners", res.SkippedListeners)
	return res, err
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
// below MinConfidence. nan reports that a Transform produced a NaN confidence.
func (s *Spreader) delivered(e Encounter) (out knowledge.Fact, nan, ok bool) {
	fact, teller, now := e.Fact, e.Teller, e.Now
	out = fact
	out.Confidence = fact.Confidence * s.cfg.DecayFactor
	out.Source = s.cfg.ReceivedKind
	out.Observed = now
	out.Prov = knowledge.Provenance{From: teller.ID(), Hops: fact.Prov.Hops + 1, EventID: fact.Prov.EventID}
	out.Private = false
	out.FadedAt = 0 // the listener has not faded this copy yet
	if out.Expiry != nil {
		x := *out.Expiry
		out.Expiry = &x
	}
	if s.Transform != nil {
		t := s.Transform(e, out)
		out.Confidence, out.Payload, out.Expiry = t.Confidence, t.Payload, t.Expiry
		if math.IsNaN(out.Confidence) {
			return knowledge.Fact{}, true, false
		}
	}
	if !(out.Confidence >= s.cfg.MinConfidence) { // also false for NaN
		return knowledge.Fact{}, false, false
	}
	out.Confidence = min(out.Confidence, 1)
	return out, false, true
}
