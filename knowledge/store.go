package knowledge

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
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
	s := &Store{cfg: cfg, merge: RankThenConfidence, facts: make(map[string]Fact)}
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
func (s *Store) Add(f Fact) (AddResult, error) {
	if f.Type == "" || f.Source == "" || math.IsNaN(f.Confidence) {
		return AddResult{}, fmt.Errorf("%w: type %q, source %q, confidence %v", ErrInvalidFact, f.Type, f.Source, f.Confidence)
	}
	if strings.Contains(f.Type+f.Subject+f.Discriminator, idSep) {
		return AddResult{}, fmt.Errorf("%w: type, subject and discriminator must not contain U+001F", ErrInvalidFact)
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
		return res, nil
	}
	if existing.Type != f.Type || existing.Subject != f.Subject {
		return AddResult{}, fmt.Errorf("%w: %q holds (%q, %q), got (%q, %q)",
			ErrIdentityMismatch, f.ID, existing.Type, existing.Subject, f.Type, f.Subject)
	}
	if s.merge(existing, f, s.rank) == Replace {
		s.facts[f.ID] = f
		res.Outcome = Replaced
	}
	return res, nil
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

// Prune deletes expired facts and returns how many it removed.
func (s *Store) Prune(now Tick) int {
	n := 0
	for id, f := range s.facts {
		if expired(f, now) {
			delete(s.facts, id)
			n++
		}
	}
	return n
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
