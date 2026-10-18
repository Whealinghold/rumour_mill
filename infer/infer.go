// Package infer derives new facts from combinations of facts a holder already
// has: "if I know X about someone and Y about them, I conclude Z".
//
// Rules are plain data (and loadable from JSON or YAML by the declare
// package); an Engine is immutable. Derived facts are ordinary knowledge
// facts with a configurable source kind and the IDs of their premises as
// provenance. The package contains no vocabulary of any setting.
//
// Rule types, subjects and IDs are not re-validated against the knowledge
// package's identity rules: a Product.Type containing the store's ID separator
// surfaces from Run as knowledge.ErrInvalidFact, not from New.
package infer

import (
	"errors"
	"fmt"
	"slices"

	"github.com/Whealinghold/rumour_mill/knowledge"
)

// Errors returned by this package. Check with errors.Is.
var (
	ErrInvalidConfig = errors.New("infer: invalid config")
	ErrInvalidRule   = errors.New("infer: invalid rule")
	ErrNilStore      = errors.New("infer: nil store")
)

// Config holds the tunables of an Engine. Use DefaultConfig and override
// fields. The zero Config is not valid (MaxPasses must be at least 1 and the
// strings cannot be empty), so start from DefaultConfig.
type Config struct {
	// MaxPasses bounds the passes per Run. 1 means a single pass with no
	// chaining; more lets derived facts feed later rules.
	MaxPasses int `json:"max_passes" yaml:"max_passes"`
	// IDPrefix starts every derived fact ID: prefix:ruleID:subject.
	IDPrefix string `json:"id_prefix" yaml:"id_prefix"`
	// DerivedKind is the source kind stamped on derived facts. It should be a
	// kind the store ranks.
	DerivedKind string `json:"derived_kind" yaml:"derived_kind"`
}

// DefaultConfig returns the documented defaults.
func DefaultConfig() Config {
	return Config{MaxPasses: 1, IDPrefix: "inferred", DerivedKind: "derived"}
}

// Validate reports every problem with c, joined; each wraps ErrInvalidConfig.
func (c Config) Validate() error {
	var errs []error
	if c.MaxPasses < 1 {
		errs = append(errs, fmt.Errorf("%w: max_passes %d must be at least 1", ErrInvalidConfig, c.MaxPasses))
	}
	if c.IDPrefix == "" {
		errs = append(errs, fmt.Errorf("%w: id_prefix is empty", ErrInvalidConfig))
	}
	if c.DerivedKind == "" {
		errs = append(errs, fmt.Errorf("%w: derived_kind is empty", ErrInvalidConfig))
	}
	return errors.Join(errs...)
}

// Requirement is one premise of a rule.
type Requirement struct {
	Type string `json:"type" yaml:"type"` // required
	// Subject "" means the rule's bound subject, shared by every requirement
	// that leaves it empty; otherwise a fixed subject ID.
	Subject string `json:"subject,omitempty" yaml:"subject,omitempty"`
	// MinConfidence is the premise's confidence floor; nil uses the store's query floor.
	MinConfidence *float64 `json:"min_confidence,omitempty" yaml:"min_confidence,omitempty"`
	// Sources limits the premise to these source kinds; empty means any.
	Sources []string `json:"sources,omitempty" yaml:"sources,omitempty"`
}

// Product is the fact a rule derives.
type Product struct {
	Type string `json:"type" yaml:"type"` // required
	// Subject "" means the bound subject; otherwise a fixed subject ID.
	Subject string `json:"subject,omitempty" yaml:"subject,omitempty"`
	TTL     int64  `json:"ttl,omitempty" yaml:"ttl,omitempty"` // ticks; 0 = never expires
	Private bool   `json:"private,omitempty" yaml:"private,omitempty"`
	Payload any    `json:"payload,omitempty" yaml:"payload,omitempty"`
}

// Rule derives Produces at Confidence whenever all Requires hold.
type Rule struct {
	ID         string        `json:"id" yaml:"id"`
	Requires   []Requirement `json:"requires" yaml:"requires"`
	Produces   Product       `json:"produces" yaml:"produces"`
	Confidence float64       `json:"confidence" yaml:"confidence"` // in (0,1]
}

// Result describes one Run.
type Result struct {
	Derived []knowledge.AddResult // facts inserted or replaced, in the order produced
	Passes  int                   // passes executed (at least 1)
}

// Engine applies a fixed set of rules to knowledge stores. It is immutable.
//
// An Engine is safe to share, but Run writes to the store it is given, and
// stores are not safe for concurrent use: the caller serialises access to
// each store. Create one with New.
type Engine struct {
	cfg   Config
	rules []Rule
}

// New validates the rules and returns an Engine. The rules are copied. All
// problems are reported together, wrapping ErrInvalidRule or ErrInvalidConfig.
func New(cfg Config, rules []Rule) (*Engine, error) {
	errs := []error{cfg.Validate()}
	seen := make(map[string]bool, len(rules))
	for i, r := range rules {
		bad := func(format string, args ...any) {
			errs = append(errs, fmt.Errorf("%w: rule %d (%q): "+format, append([]any{ErrInvalidRule, i, r.ID}, args...)...))
		}
		if r.ID == "" {
			bad("empty ID")
		} else if seen[r.ID] {
			bad("duplicate ID")
		}
		seen[r.ID] = true
		if len(r.Requires) == 0 {
			bad("no requirements")
		}
		allFixed := len(r.Requires) > 0
		for j, q := range r.Requires {
			if q.Type == "" {
				bad("requirement %d has no type", j)
			}
			if q.MinConfidence != nil && !(*q.MinConfidence >= 0 && *q.MinConfidence <= 1) {
				bad("requirement %d min_confidence %v outside [0,1]", j, *q.MinConfidence)
			}
			allFixed = allFixed && q.Subject != ""
		}
		if allFixed && r.Produces.Subject == "" {
			bad("every requirement has a fixed subject, so the product needs one too")
		}
		if r.Produces.Type == "" {
			bad("product has no type")
		}
		if !(r.Confidence > 0 && r.Confidence <= 1) {
			bad("confidence %v outside (0,1]", r.Confidence)
		}
		if r.Produces.TTL < 0 {
			bad("negative ttl")
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &Engine{cfg: cfg, rules: cloneRules(rules)}, nil
}

func cloneRules(rules []Rule) []Rule {
	out := slices.Clone(rules)
	for i := range out {
		out[i].Requires = slices.Clone(out[i].Requires)
		for j := range out[i].Requires {
			q := &out[i].Requires[j]
			q.Sources = slices.Clone(q.Sources)
			if q.MinConfidence != nil {
				v := *q.MinConfidence
				q.MinConfidence = &v
			}
		}
	}
	return out
}

// Run applies the rules to store at tick now and returns the facts it derived.
//
// Each pass evaluates every rule against the store as it stood at the start of
// the pass, so rule order within a pass does not matter, then adds the derived
// facts. Run stops after MaxPasses, or earlier when a pass inserts or replaces
// nothing. Re-deriving an unchanged conclusion is rejected by the store's
// merge rules (equal rank and confidence keep the existing fact), so Run is
// idempotent and always terminates: derived IDs are limited to
// rules x subjects, and each one can only be replaced by a higher confidence.
//
// A rule matches a requirement with a fact of that type whose confidence meets
// the requirement's floor, whose source is allowed, and, for a fixed subject,
// whose subject equals it. Requirements with no subject share one bound
// subject: the rule fires once for every subject that satisfies all of them
// (in ascending order). Fixed-subject requirements are checked independently
// of it. A derived fact takes the bound subject unless the product fixes one.
func (e *Engine) Run(store *knowledge.Store, now knowledge.Tick) (Result, error) {
	if store == nil {
		return Result{}, ErrNilStore
	}
	var res Result
	for pass := 1; pass <= e.cfg.MaxPasses; pass++ {
		res.Passes = pass
		var batch []knowledge.Fact
		for _, r := range e.rules {
			batch = append(batch, e.fire(store, r, now)...)
		}
		changed := 0
		var errs []error
		for _, f := range batch {
			add, err := store.Add(f)
			if err != nil {
				errs = append(errs, fmt.Errorf("infer: %s: %w", f.ID, err))
				continue
			}
			if add.Outcome != knowledge.Rejected {
				res.Derived = append(res.Derived, add)
				changed++
			}
		}
		if len(errs) > 0 {
			return res, errors.Join(errs...)
		}
		if changed == 0 {
			break
		}
	}
	return res, nil
}

// fire returns the facts rule r derives from store, one per satisfying subject.
func (e *Engine) fire(store *knowledge.Store, r Rule, now knowledge.Tick) []knowledge.Fact {
	best := func(q Requirement) []knowledge.Fact {
		return store.Find(knowledge.Query{Type: q.Type, Subject: q.Subject, Sources: q.Sources, MinConfidence: q.MinConfidence}, now)
	}
	strongest := func(facts []knowledge.Fact) knowledge.Fact { // Find is ascending by ID, so ties keep the lowest
		top := facts[0]
		for _, f := range facts[1:] {
			if f.Confidence > top.Confidence {
				top = f
			}
		}
		return top
	}

	var premises []string          // best fact per fixed requirement
	var unbound [][]knowledge.Fact // matches per unbound requirement
	for _, q := range r.Requires {
		matches := best(q)
		if len(matches) == 0 {
			return nil
		}
		if q.Subject != "" {
			premises = append(premises, strongest(matches).ID)
		} else {
			unbound = append(unbound, matches)
		}
	}

	candidates := []string{r.Produces.Subject}
	if len(unbound) > 0 {
		candidates = subjectsInAll(unbound)
	}
	var out []knowledge.Fact
	for _, s := range candidates {
		ids := slices.Clone(premises)
		for _, matches := range unbound {
			ids = append(ids, strongest(forSubject(matches, s)).ID)
		}
		slices.Sort(ids)
		subject := s
		if r.Produces.Subject != "" {
			subject = r.Produces.Subject
		}
		f := knowledge.Fact{
			ID:         e.cfg.IDPrefix + ":" + r.ID + ":" + subject,
			Type:       r.Produces.Type,
			Subject:    subject,
			Payload:    r.Produces.Payload,
			Confidence: r.Confidence,
			Source:     e.cfg.DerivedKind,
			Prov:       knowledge.Provenance{Premises: slices.Compact(ids)},
			Observed:   now,
			Private:    r.Produces.Private,
		}
		if r.Produces.TTL > 0 {
			expiry := now + knowledge.Tick(r.Produces.TTL)
			f.Expiry = &expiry
		}
		out = append(out, f)
	}
	return out
}

func forSubject(facts []knowledge.Fact, subject string) []knowledge.Fact {
	return slices.DeleteFunc(slices.Clone(facts), func(f knowledge.Fact) bool { return f.Subject != subject })
}

// subjectsInAll returns, ascending, the subjects present in every match list.
func subjectsInAll(lists [][]knowledge.Fact) []string {
	var out []string
	for _, f := range lists[0] {
		s := f.Subject
		if slices.Contains(out, s) {
			continue
		}
		everywhere := true
		for _, l := range lists[1:] {
			if !slices.ContainsFunc(l, func(g knowledge.Fact) bool { return g.Subject == s }) {
				everywhere = false
				break
			}
		}
		if everywhere {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}
