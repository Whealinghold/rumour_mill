// Package rules is a layered rule registry and pipeline. When an event reaches
// an entity, Process runs the rules that match, layer by layer, in priority
// order, with optional stop semantics.
//
// Everything domain-specific is caller-defined: the context type C (typically
// the entity, the event and the world bundled together), the ordered layers,
// the scope keys inside each layer, event types, and the conditions and
// actions themselves. The package contains no vocabulary of any setting and
// does not import the knowledge package.
package rules

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/Whealinghold/rumour_mill/logic"
)

// Errors returned by this package. Check with errors.Is.
var (
	ErrInvalidConfig = errors.New("rules: invalid config")
	ErrInvalidLayer  = errors.New("rules: invalid layer")
	ErrUnknownLayer  = errors.New("rules: unknown layer")
	ErrInvalidRule   = errors.New("rules: invalid rule")
	ErrDuplicateRule = errors.New("rules: duplicate rule")
)

// Config holds the tunables of a Registry. Use DefaultConfig and override fields.
type Config struct {
	// OnActionError is what Process does when a rule's action returns an error:
	// "continue" (also the meaning of the empty string, so the zero Config works)
	// records the error and carries on, "stop" returns immediately.
	OnActionError string `json:"on_action_error" yaml:"on_action_error"`
}

// OnActionError values.
const (
	ContinueOnError = "continue"
	StopOnError     = "stop"
)

// DefaultConfig returns the documented defaults.
func DefaultConfig() Config { return Config{OnActionError: ContinueOnError} }

// Validate reports every problem with c, joined; each wraps ErrInvalidConfig.
func (c Config) Validate() error {
	if c.OnActionError != "" && c.OnActionError != ContinueOnError && c.OnActionError != StopOnError {
		return fmt.Errorf("%w: on_action_error %q must be empty, %q or %q",
			ErrInvalidConfig, c.OnActionError, ContinueOnError, StopOnError)
	}
	return nil
}

// Layer is one tier of rules. Layers are ordered; earlier layers are processed first.
type Layer[C any] struct {
	Name string
	// Scopes returns the scope keys ctx belongs to in this layer (a group name,
	// an entity ID, a role...). It may return several keys or none. Nil means
	// the layer is universal: every context has the single key "". Scopes is
	// called each time Process reaches the layer, so it sees changes made to
	// ctx by earlier layers' actions.
	Scopes func(ctx C) []string
}

// StopMode says what a fired rule does to the rules after it.
type StopMode string

// The stop modes.
const (
	StopNone  StopMode = ""      // continue
	StopLayer StopMode = "layer" // skip the remaining rules in this layer
	StopAll   StopMode = "all"   // skip the remaining rules in this and all later layers
)

// Rule reacts to one event type.
type Rule[C any] struct {
	ID       string             // unique within (layer, scope); required
	Event    string             // event type this rule reacts to; required; exact match
	When     logic.Condition[C] // nil = always
	Do       logic.Action[C]    // nil = no-op (a rule may exist only to stop propagation)
	Priority int                // lower runs first
	Stop     StopMode           // applied only when the rule fires
}

// Result describes one Process call.
type Result struct {
	Fired []Fired // in execution order
	// Stopped is StopAll if Process ended early, else StopLayer if some layer was
	// cut short, else StopNone.
	Stopped StopMode
}

// Fired identifies a rule that fired.
type Fired struct{ Layer, Scope, RuleID string }

type ruleKey struct{ layer, scope, event string }
type idKey struct{ layer, scope, id string }

type entry[C any] struct {
	rule   Rule[C]
	scope  string
	seq    uint64
	keyPos int
}

// Registry stores rules by layer, scope and event type and runs them.
//
// A Registry is safe for concurrent use. Register, Unregister take a write
// lock; Process holds a read lock only while copying the matching rules and
// runs no lock while caller code executes, so conditions and actions may call
// Register or Unregister (the change applies from the next Process).
type Registry[C any] struct {
	cfg    Config
	layers []Layer[C]
	index  map[string]int

	mu    sync.RWMutex
	byKey map[ruleKey][]entry[C]
	ids   map[idKey]string // rule ID -> its event type
	seq   uint64
}

// NewRegistry returns an empty registry with the given ordered layers. It
// returns ErrInvalidConfig for a bad cfg and ErrInvalidLayer for an empty or
// duplicate layer name.
func NewRegistry[C any](cfg Config, layers ...Layer[C]) (*Registry[C], error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	r := &Registry[C]{
		cfg:    cfg,
		layers: slices.Clone(layers),
		index:  make(map[string]int, len(layers)),
		byKey:  make(map[ruleKey][]entry[C]),
		ids:    make(map[idKey]string),
	}
	for i, l := range layers {
		if l.Name == "" {
			return nil, fmt.Errorf("%w: layer %d has no name", ErrInvalidLayer, i)
		}
		if _, dup := r.index[l.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate layer name %q", ErrInvalidLayer, l.Name)
		}
		r.index[l.Name] = i
	}
	return r, nil
}

// Register adds rule to the given layer and scope. A universal layer (nil
// Scopes) takes only the scope "". It returns ErrUnknownLayer for a layer the
// registry lacks, ErrInvalidRule for an empty ID or Event, an unknown Stop, or
// a non-empty scope on a universal layer, and ErrDuplicateRule if the
// (layer, scope, ID) is already registered.
func (r *Registry[C]) Register(layer, scope string, rule Rule[C]) error {
	i, ok := r.index[layer]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownLayer, layer)
	}
	switch {
	case rule.ID == "" || rule.Event == "":
		return fmt.Errorf("%w: rule needs an ID and an Event", ErrInvalidRule)
	case rule.Stop != StopNone && rule.Stop != StopLayer && rule.Stop != StopAll:
		return fmt.Errorf("%w: %q has unknown stop mode %q", ErrInvalidRule, rule.ID, rule.Stop)
	case r.layers[i].Scopes == nil && scope != "":
		return fmt.Errorf("%w: %q is in universal layer %q, which takes only scope \"\"", ErrInvalidRule, rule.ID, layer)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	id := idKey{layer, scope, rule.ID}
	if _, dup := r.ids[id]; dup {
		return fmt.Errorf("%w: %s/%s/%s", ErrDuplicateRule, layer, scope, rule.ID)
	}
	r.ids[id] = rule.Event
	k := ruleKey{layer, scope, rule.Event}
	r.seq++
	r.byKey[k] = append(r.byKey[k], entry[C]{rule: rule, scope: scope, seq: r.seq})
	return nil
}

// Unregister removes a rule and reports whether it existed.
func (r *Registry[C]) Unregister(layer, scope, ruleID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := idKey{layer, scope, ruleID}
	event, ok := r.ids[id]
	if !ok {
		return false
	}
	delete(r.ids, id)
	k := ruleKey{layer, scope, event}
	r.byKey[k] = slices.DeleteFunc(slices.Clone(r.byKey[k]), func(e entry[C]) bool { return e.rule.ID == ruleID })
	if len(r.byKey[k]) == 0 {
		delete(r.byKey, k)
	}
	return true
}

// gather copies the rules for a layer's scope keys, merged and ordered by
// priority, then position of the scope key, then registration order.
func (r *Registry[C]) gather(layer string, keys []string, event string) []entry[C] {
	var list []entry[C]
	r.mu.RLock()
	for pos, key := range keys {
		for _, e := range r.byKey[ruleKey{layer, key, event}] {
			e.keyPos = pos
			list = append(list, e)
		}
	}
	r.mu.RUnlock()
	slices.SortFunc(list, func(a, b entry[C]) int {
		return cmp.Or(cmp.Compare(a.rule.Priority, b.rule.Priority), cmp.Compare(a.keyPos, b.keyPos), cmp.Compare(a.seq, b.seq))
	})
	return list
}

// scopeKeys returns the layer's keys for ctx with duplicates dropped (first wins).
func scopeKeys[C any](l Layer[C], ctx C) []string {
	if l.Scopes == nil {
		return []string{""}
	}
	keys := l.Scopes(ctx)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// Process runs every rule that matches ctx and eventType.
//
// Layers run in registration order. Within a layer the rules of all of ctx's
// scope keys are merged and ordered by Priority (lower first), then by the
// position of the scope key, then by registration order. A rule fires when its
// When is nil or true; it then runs Do and applies its Stop mode: StopLayer
// skips the rest of its layer, StopAll ends Process. A rule whose condition is
// false never stops anything.
//
// Errors from Do are joined into the returned error. With OnActionError
// "continue" processing carries on (a rule that fired but failed still applies
// its Stop); with "stop" Process returns right after the failing rule. An event
// type with no rules is not an error.
func (r *Registry[C]) Process(ctx C, eventType string) (Result, error) {
	var res Result
	var errs []error
layers:
	for _, layer := range r.layers {
		for _, e := range r.gather(layer.Name, scopeKeys(layer, ctx), eventType) {
			if e.rule.When != nil && !e.rule.When(ctx) {
				continue
			}
			res.Fired = append(res.Fired, Fired{layer.Name, e.scope, e.rule.ID})
			if e.rule.Do != nil {
				if err := e.rule.Do(ctx); err != nil {
					errs = append(errs, fmt.Errorf("rules: %s/%s/%s: %w", layer.Name, e.scope, e.rule.ID, err))
					if r.cfg.OnActionError == StopOnError {
						return res, errors.Join(errs...)
					}
				}
			}
			switch e.rule.Stop {
			case StopAll:
				res.Stopped = StopAll
				break layers
			case StopLayer:
				if res.Stopped == StopNone {
					res.Stopped = StopLayer
				}
				continue layers
			}
		}
	}
	return res, errors.Join(errs...)
}
