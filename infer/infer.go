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
	"log/slog"
	"slices"
	"time"

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
	// Truncated reports that MaxPasses was greater than 1 and the last pass
	// still inserted or replaced facts, so chaining may have been cut short.
	// It is conservative: a chain that reaches its fixpoint exactly on the last
	// pass is also flagged, and MaxPasses of 1 never sets it.
	Truncated bool
}

// Engine applies a fixed set of rules to knowledge stores. It is immutable.
//
// An Engine is safe to share, but Run writes to the store it is given, and
// stores are not safe for concurrent use: the caller serialises access to
// each store. Create one with New.
type Engine struct {
	cfg   Config
	rules []Rule
	hooks Hooks
	log   *slog.Logger
	now   func() time.Time
}

// Hooks are optional observers of an Engine. Any field may be nil. Hooks are
// called synchronously on the caller's goroutine, must treat their arguments
// as read-only, and must not change what Run does. A Hooks value is
// plain data; the funcs must be safe for concurrent use if the Engine is shared.
type Hooks struct {
	// OnRun is called once at the end of every Run, with its Result and the
	// error Run returns (nil on success). That includes Runs that fail with
	// store errors, and the ErrNilStore call (with a zero Result), so error
	// counters stay accurate. Exhausting MaxPasses is not an error.
	OnRun func(Result, error)
	// OnRunDone is called after OnRun on every Run, including the ErrNilStore
	// call, with the elapsed time (never negative). It fires only when a clock
	// was given with WithClock.
	OnRunDone func(time.Duration)
}

// Option configures an Engine at construction. Options are applied once, in New,
// and are not safe to share with a running Engine; the Option func itself holds
// no state and may be reused across New calls.
type Option func(*Engine)

// WithHooks adds observer hooks. It may be given several times; for each
// field the earlier hook runs first, then this one. Nil fields are ignored.
func WithHooks(h Hooks) Option {
	return func(e *Engine) {
		if h.OnRun != nil {
			prev, next := e.hooks.OnRun, h.OnRun
			e.hooks.OnRun = func(r Result, err error) {
				if prev != nil {
					prev(r, err)
				}
				next(r, err)
			}
		}
		if h.OnRunDone != nil {
			prev, next := e.hooks.OnRunDone, h.OnRunDone
			e.hooks.OnRunDone = func(d time.Duration) {
				if prev != nil {
					prev(d)
				}
				next(d)
			}
		}
	}
}

// WithClock injects the clock used to time Run for Hooks.OnRunDone. It is read
// at the start of Run and just before OnRun; without it the clock is never
// read and OnRunDone never fires. A nil now is ignored.
func WithClock(now func() time.Time) Option {
	return func(e *Engine) {
		if now != nil {
			e.now = now
		}
	}
}

// WithLogger sets the logger. Run logs one Debug line per call (passes,
// derived, truncated, and by_rule: facts inserted or replaced per rule ID),
// a Warn when it returns an error and a Warn when the run was truncated. A nil logger is ignored; the
// default discards everything.
func WithLogger(l *slog.Logger) Option {
	return func(e *Engine) {
		if l != nil {
			e.log = l
		}
	}
}

// New validates the rules and returns an Engine. The rules are copied. All
// problems are reported together, wrapping ErrInvalidRule or ErrInvalidConfig.
// Options are applied in the order given.
func New(cfg Config, rules []Rule, opts ...Option) (*Engine, error) {
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
	e := &Engine{cfg: cfg, rules: cloneRules(rules), log: slog.New(slog.DiscardHandler)}
	for _, o := range opts {
		if o != nil {
			o(e)
		}
	}
	return e, nil
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
//
// The OnRun hook and the logger observe every call, including the nil-store
// error.
func (e *Engine) Run(store *knowledge.Store, now knowledge.Tick) (Result, error) {
	var start time.Time
	if e.now != nil {
		start = e.now()
	}
	res, byRule, err := e.run(store, now)
	switch {
	case err != nil:
		e.log.Warn("infer run failed", "passes", res.Passes, "derived", len(res.Derived), "err", err)
	default:
		e.log.Debug("infer run", "passes", res.Passes, "derived", len(res.Derived), "truncated", res.Truncated, "by_rule", byRule)
	}
	if res.Truncated {
		e.log.Warn("infer run truncated", "passes", res.Passes, "max_passes", e.cfg.MaxPasses, "derived", len(res.Derived))
	}
	var end time.Time
	if e.now != nil {
		end = e.now()
	}
	if e.hooks.OnRun != nil {
		e.hooks.OnRun(res, err)
	}
	if e.now != nil && e.hooks.OnRunDone != nil {
		e.hooks.OnRunDone(max(end.Sub(start), 0))
	}
	return res, err
}

// run also returns, for logging only, how many facts each rule inserted or replaced.
func (e *Engine) run(store *knowledge.Store, now knowledge.Tick) (Result, map[string]int, error) {
	if store == nil {
		return Result{}, nil, ErrNilStore
	}
	var res Result
	byRule := map[string]int{}
	for pass := 1; pass <= e.cfg.MaxPasses; pass++ {
		res.Passes = pass
		var batch []knowledge.Fact
		var owner []string // rule ID per batch fact
		for _, r := range e.rules {
			fs := e.fire(store, r, now)
			batch = append(batch, fs...)
			for range fs {
				owner = append(owner, r.ID)
			}
		}
		changed := 0
		var errs []error
		for i, f := range batch {
			add, err := store.Add(f)
			if err != nil {
				errs = append(errs, fmt.Errorf("infer: %s: %w", f.ID, err))
				continue
			}
			if add.Outcome != knowledge.Rejected {
				res.Derived = append(res.Derived, add)
				byRule[owner[i]]++
				changed++
			}
		}
		if len(errs) > 0 {
			return res, byRule, errors.Join(errs...)
		}
		if changed == 0 {
			break
		}
		if pass == e.cfg.MaxPasses && pass > 1 {
			res.Truncated = true
		}
	}
	return res, byRule, nil
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
