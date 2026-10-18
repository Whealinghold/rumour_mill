package declare

import (
	"errors"
	"fmt"

	"github.com/Whealinghold/rumour_mill/logic"
	"github.com/Whealinghold/rumour_mill/rules"
)

// RulesConfig is rules.Config plus the one tunable only the loader needs. In a
// document both appear side by side under rules:.
type RulesConfig struct {
	rules.Config `json:",inline" yaml:",inline"`
	// DefaultPriority is given to a rule in a document that omits priority.
	DefaultPriority int `json:"default_priority" yaml:"default_priority"`
}

// DefaultRulesConfig returns the documented defaults (priority 100).
func DefaultRulesConfig() RulesConfig {
	return RulesConfig{Config: rules.DefaultConfig(), DefaultPriority: 100}
}

// RuleSpec is one rule as written in a document.
type RuleSpec struct {
	ID       string         `json:"id" yaml:"id"`
	Layer    string         `json:"layer" yaml:"layer"`
	Scope    string         `json:"scope,omitempty" yaml:"scope,omitempty"` // omit for universal layers
	Event    string         `json:"event" yaml:"event"`
	Priority *int           `json:"priority,omitempty" yaml:"priority,omitempty"` // nil: RulesConfig.DefaultPriority
	Stop     rules.StopMode `json:"stop,omitempty" yaml:"stop,omitempty"`
	When     *Node          `json:"when,omitempty" yaml:"when,omitempty"` // nil: always
	Do       []Node         `json:"do,omitempty" yaml:"do,omitempty"`     // run as a sequence
}

// LoadRules builds the rules in specs from cat and registers them in reg.
//
// Every spec is resolved first and all resolution errors are reported
// together, each with its path (for example rules[3].when.of[1]); if any
// fails, nothing is registered. If registration itself fails (an unknown
// layer, a duplicate ID), the rules registered so far by this call are removed
// again and that error is returned. Concurrent Process calls may briefly see a
// partial load.
func LoadRules[C any](reg *rules.Registry[C], cfg RulesConfig, specs []RuleSpec, cat *Catalog[C]) error {
	type ready struct {
		layer, scope string
		rule         rules.Rule[C]
	}
	var errs []error
	var batch []ready
	for i, s := range specs {
		path := fmt.Sprintf("rules[%d]", i)
		var when logic.Condition[C]
		if s.When != nil {
			var err error
			if when, err = cat.condition(*s.When, path+".when"); err != nil {
				errs = append(errs, err)
			}
		}
		as, err := cat.actions(s.Do, path+".do")
		if err != nil {
			errs = append(errs, err)
		}
		priority := cfg.DefaultPriority
		if s.Priority != nil {
			priority = *s.Priority
		}
		var do logic.Action[C]
		if len(s.Do) > 0 {
			do = logic.Sequence(as...)
		}
		batch = append(batch, ready{s.Layer, s.Scope, rules.Rule[C]{ID: s.ID, Event: s.Event, When: when, Do: do, Priority: priority, Stop: s.Stop}})
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	for i, r := range batch {
		if err := reg.Register(r.layer, r.scope, r.rule); err != nil {
			for _, done := range batch[:i] {
				reg.Unregister(done.layer, done.scope, done.rule.ID)
			}
			return fmt.Errorf("rules[%d] (%s): %w", i, r.rule.ID, err)
		}
	}
	return nil
}
