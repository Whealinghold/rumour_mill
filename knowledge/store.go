package knowledge

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"slices"
	"strings"
	"time"
)

// idSep separates the parts of a derived fact ID. Add rejects it in those parts.
const idSep = "\x1f"

// Store holds one holder's facts.
//
// A Store is not safe for concurrent use; wrap it with your own lock. Create
// one with NewStore.
//
// Find and All sort the fact IDs on every call, O(n log n). That is fine for
// NPC-sized stores; do not use a Store as a large database.
type Store struct {
	cfg   Config
	merge MergePolicy
	facts map[string]Fact
	fader Fader
	clock func() time.Time
	hooks []Hooks
	log   *slog.Logger
}

// Option configures a Store at construction.
type Option func(*Store)

// WithMergePolicy replaces the default RankThenConfidence merge policy.
func WithMergePolicy(p MergePolicy) Option {
	return func(s *Store) {
		if p != nil {
			s.merge = p
		}
	}
}

// NewStore returns an empty store. It returns an error wrapping
// ErrInvalidConfig if cfg is invalid. The store keeps its own copy of cfg.
func NewStore(cfg Config, opts ...Option) (*Store, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.SourceRanks = maps.Clone(cfg.SourceRanks)
	cfg.Fade.HalfLives = maps.Clone(cfg.Fade.HalfLives)
	s := &Store{cfg: cfg, merge: RankThenConfidence, facts: make(map[string]Fact), log: slog.New(slog.DiscardHandler)}
	for _, o := range opts {
		o(s)
	}
	if s.fader == nil {
		s.fader = exponentialFader(s.cfg.Fade)
	}
	return s, nil
}

func (s *Store) rank(source string) int {
	if r, ok := s.cfg.SourceRanks[source]; ok {
		return r
	}
	return s.cfg.UnrankedRank
}

// Add stores f, merging with an existing fact of the same ID. An empty ID is
// derived from Type, Subject and Discriminator, so repeated tellings of the
// same claim merge. Confidence is clamped to [0,1].
//
// It returns ErrInvalidFact for an empty Type or Source or a NaN Confidence,
// and ErrIdentityMismatch if the ID exists with a different Type or Subject.
func (s *Store) Add(in Fact) (AddResult, error) {
	f, res, err := s.add(in)
	if err != nil {
		reason := AddErrorInvalid
		if errors.Is(err, ErrIdentityMismatch) {
			reason = AddErrorIdentityMismatch
			s.log.Warn("knowledge: add identity mismatch", "fact", f.ID, "error", err)
		} else {
			s.log.Warn("knowledge: add invalid fact", "type", f.Type, "source", f.Source, "error", err)
		}
		for _, h := range s.hooks {
			if h.OnAddError != nil {
				h.OnAddError(clone(in), reason, err)
			}
		}
		return res, err
	}
	if len(res.Evicted) > 0 {
		s.log.Warn("knowledge: add evicted facts", "fact", res.ID, "outcome", res.Outcome.String(), "evicted", len(res.Evicted))
	}
	for _, h := range s.hooks {
		if h.OnAdd != nil {
			h.OnAdd(clone(f), res)
		}
	}
	return res, nil
}

// add does the work of Add and also returns the normalised incoming fact.
func (s *Store) add(f Fact) (Fact, AddResult, error) {
	if f.Type == "" || f.Source == "" || math.IsNaN(f.Confidence) {
		return f, AddResult{}, fmt.Errorf("%w: type %q, source %q, confidence %v", ErrInvalidFact, f.Type, f.Source, f.Confidence)
	}
	if strings.Contains(f.Type+f.Subject+f.Discriminator, idSep) {
		return f, AddResult{}, fmt.Errorf("%w: type, subject and discriminator must not contain U+001F", ErrInvalidFact)
	}
	f.Confidence = min(max(f.Confidence, 0), 1)
	if f.ID == "" {
		f.ID = f.Type + idSep + f.Subject + idSep + f.Discriminator
	}
	f = clone(f)
	res := AddResult{ID: f.ID}

	existing, ok := s.facts[f.ID]
	if !ok {
		s.facts[f.ID] = f
		res.Outcome = Inserted
		res.Evicted = s.enforceCap()
		if slices.Contains(res.Evicted, f.ID) {
			res.Outcome = Rejected
		}
		res.Size = len(s.facts)
		return f, res, nil
	}
	if existing.Type != f.Type || existing.Subject != f.Subject {
		return f, AddResult{}, fmt.Errorf("%w: %q holds (%q, %q), got (%q, %q)",
			ErrIdentityMismatch, f.ID, existing.Type, existing.Subject, f.Type, f.Subject)
	}
	if s.merge(existing, f, s.rank) == Replace {
		s.facts[f.ID] = f
		res.Outcome = Replaced
	}
	res.Size = len(s.facts)
	return f, res, nil
}

// enforceCap evicts the weakest facts while the store is over MaxFacts:
// lowest rank, then lowest confidence, then oldest Observed, then ascending ID.
func (s *Store) enforceCap() []string {
	if s.cfg.MaxFacts == 0 {
		return nil
	}
	var evicted []string
	for len(s.facts) > s.cfg.MaxFacts {
		var weakest *Fact
		for _, id := range slices.Sorted(maps.Keys(s.facts)) {
			f := s.facts[id]
			if weakest == nil || s.weaker(f, *weakest) {
				weakest = &f
			}
		}
		delete(s.facts, weakest.ID)
		evicted = append(evicted, weakest.ID)
	}
	return evicted
}

// weaker reports whether a is strictly weaker than b. Callers iterate in
// ascending ID order, so equal facts resolve to the smallest ID.
func (s *Store) weaker(a, b Fact) bool {
	if ra, rb := s.rank(a.Source), s.rank(b.Source); ra != rb {
		return ra < rb
	}
	if a.Confidence != b.Confidence {
		return a.Confidence < b.Confidence
	}
	return a.Observed < b.Observed
}

// Get returns the fact with the given ID, ignoring the confidence floor and
// expiry.
func (s *Store) Get(id string) (Fact, bool) {
	f, ok := s.facts[id]
	return clone(f), ok
}

// Find returns the unexpired facts matching q at tick now, in ascending ID
// order. A nil q.MinConfidence uses Config.QueryFloor. Matching is exact and
// case-sensitive.
func (s *Store) Find(q Query, now Tick) []Fact {
	if s.clock != nil {
		start := s.clock()
		defer func() { s.duration(OpFind, start) }()
	}
	floor := s.cfg.QueryFloor
	if q.MinConfidence != nil {
		floor = *q.MinConfidence
	}
	var out []Fact
	for _, id := range slices.Sorted(maps.Keys(s.facts)) {
		f := s.facts[id]
		switch {
		case q.Type != "" && f.Type != q.Type,
			q.Subject != "" && f.Subject != q.Subject,
			len(q.Sources) > 0 && !slices.Contains(q.Sources, f.Source),
			f.Confidence < floor,
			expired(f, now):
			continue
		}
		out = append(out, clone(f))
	}
	return out
}

// Prune deletes expired facts and returns how many it removed. It logs at
// Debug and calls Hooks.OnPrune on every call, including when nothing expired.
func (s *Store) Prune(now Tick) int {
	if s.clock != nil {
		start := s.clock()
		defer func() { s.duration(OpPrune, start) }()
	}
	n := 0
	for id, f := range s.facts {
		if expired(f, now) {
			delete(s.facts, id)
			n++
		}
	}
	s.log.Debug("knowledge: prune", "removed", n, "size", len(s.facts))
	for _, h := range s.hooks {
		if h.OnPrune != nil {
			h.OnPrune(PruneResult{Removed: n, Size: len(s.facts)})
		}
	}
	return n
}

// Len returns the number of facts held, including expired and low-confidence
// ones. It is O(1) and, like the rest of Store, not safe for concurrent use.
func (s *Store) Len() int { return len(s.facts) }

// duration reports the time since start to OnDuration hooks; only called when
// a clock is set.
func (s *Store) duration(op string, start time.Time) {
	d := max(s.clock().Sub(start), 0)
	for _, h := range s.hooks {
		if h.OnDuration != nil {
			h.OnDuration(op, d)
		}
	}
}

// PruneResult reports one Prune call to Hooks.OnPrune. It is a plain value,
// safe from any goroutine once delivered.
type PruneResult struct {
	Removed int // facts deleted
	Size    int // Len() after the prune
}

// Add error reasons passed to Hooks.OnAddError.
const (
	AddErrorInvalid          = "invalid"
	AddErrorIdentityMismatch = "identity_mismatch"
)

// Operations named in Hooks.OnDuration.
const (
	OpFind  = "find"
	OpFade  = "fade"
	OpPrune = "prune"
)

// AddErrorReasons returns a fresh slice of every reason OnAddError can report,
// for seeding metric series. Safe for concurrent use.
func AddErrorReasons() []string { return []string{AddErrorInvalid, AddErrorIdentityMismatch} }

// WithClock injects the clock used to time Find, Fade and Prune for
// Hooks.OnDuration; nil is ignored. Without a clock the Store never reads time
// and OnDuration never fires.
func WithClock(now func() time.Time) Option {
	return func(s *Store) {
		if now != nil {
			s.clock = now
		}
	}
}

// All returns every fact, including expired and low-confidence ones, in
// ascending ID order. Use it for persistence and debugging.
func (s *Store) All() []Fact {
	out := make([]Fact, 0, len(s.facts))
	for _, id := range slices.Sorted(maps.Keys(s.facts)) {
		out = append(out, clone(s.facts[id]))
	}
	return out
}

func expired(f Fact, now Tick) bool { return f.Expiry != nil && *f.Expiry <= now }

// clone copies the parts of f a caller could otherwise alias.
func clone(f Fact) Fact {
	f.Prov.Premises = slices.Clone(f.Prov.Premises)
	if f.Expiry != nil {
		e := *f.Expiry
		f.Expiry = &e
	}
	return f
}

// Hooks observe a Store. Nil fields are ignored. Hooks run synchronously on the
// caller's goroutine, must not call back into the Store, and must treat their
// arguments as read-only. Observing never changes what the Store does.
type Hooks struct {
	// OnAdd runs after every successful Add, whoever called it (add_fact, spread
	// delivery, inference), once the cap is enforced. f.ID equals r.ID. It does
	// not run when Add returns an error. A Rejected result still counts as a
	// successful Add.
	OnAdd func(f Fact, r AddResult)
	// OnAddError runs once for every Add that returns an error, before Add
	// returns. f is a copy of the fact as passed (not normalised); reason is
	// AddErrorInvalid or AddErrorIdentityMismatch. OnAdd does not run.
	OnAddError func(f Fact, reason string, err error)
	// OnFade runs at the end of every Fade, also when no Fader is wired (then
	// r.Wired is false and only Size is set).
	OnFade func(r FadeResult)
	// OnPrune runs at the end of every Prune, including when Removed is 0.
	OnPrune func(r PruneResult)
	// OnDuration runs after an operation's own hook, with op one of OpFind,
	// OpFade, OpPrune and the elapsed time floored at 0. It fires only if
	// WithClock was given.
	OnDuration func(op string, d time.Duration)
}

// WithHooks adds h to the Store. It is repeatable: hooks run in the order given.
func WithHooks(h Hooks) Option {
	return func(s *Store) { s.hooks = append(s.hooks, h) }
}

// WithLogger sets the Store's logger; nil is ignored. The default discards
// everything. It logs at Warn when an Add evicts facts or hits
// ErrIdentityMismatch or ErrInvalidFact (never the payload), at Warn when a Fade finds invalid Fader results, and at
// Debug a summary of each Prune and of each Fade.
func WithLogger(l *slog.Logger) Option {
	return func(s *Store) {
		if l != nil {
			s.log = l
		}
	}
}
