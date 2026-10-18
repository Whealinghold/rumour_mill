package declare

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Whealinghold/rumour_mill/logic"
)

// Node is one expression in a data file. The shape depends on Use:
//
//	{use: <registered name>, args: {...}}        a condition or action the caller registered
//	{use: all|any, of: [cond...]}                condition combinators
//	{use: not, of: [cond]}                       exactly one child
//	{use: seq, of: [action...]}                  run in order
//	{use: if, cond: cond, then: action, else: action}   else is optional
//
// Whether a node is a condition or an action is decided by where it appears.
type Node struct {
	Use  string `json:"use" yaml:"use"`
	Args Args   `json:"args,omitempty" yaml:"args,omitempty"`
	Of   []Node `json:"of,omitempty" yaml:"of,omitempty"`
	Cond *Node  `json:"cond,omitempty" yaml:"cond,omitempty"`
	Then *Node  `json:"then,omitempty" yaml:"then,omitempty"`
	Else *Node  `json:"else,omitempty" yaml:"else,omitempty"`
}

// reserved are the combinator names; callers cannot register them.
var reserved = map[string]bool{"all": true, "any": true, "not": true, "seq": true, "if": true}

// Catalog maps names to factories for conditions and actions over a
// caller-defined context type C. Conditions and actions have separate
// namespaces. A Catalog is an instance: nothing is registered globally.
//
// A Catalog is safe for concurrent use. Create one with NewCatalog.
type Catalog[C any] struct {
	mu    sync.RWMutex
	conds map[string]func(Args) (logic.Condition[C], error)
	acts  map[string]func(Args) (logic.Action[C], error)
}

// NewCatalog returns an empty catalog that knows only the reserved combinators.
func NewCatalog[C any]() *Catalog[C] {
	return &Catalog[C]{
		conds: make(map[string]func(Args) (logic.Condition[C], error)),
		acts:  make(map[string]func(Args) (logic.Action[C], error)),
	}
}

func checkName(name string, taken bool) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: empty name", ErrBadName)
	case reserved[name]:
		return fmt.Errorf("%w: %q is a reserved combinator", ErrDuplicateName, name)
	case taken:
		return fmt.Errorf("%w: %q", ErrDuplicateName, name)
	}
	return nil
}

// RegisterCondition makes a condition available to data as use: name. The
// factory receives the node's args and should finish with args.Unknown(...).
// It returns ErrBadName for an empty name or nil factory and ErrDuplicateName
// for a reserved or already registered condition name.
func (c *Catalog[C]) RegisterCondition(name string, factory func(Args) (logic.Condition[C], error)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := checkName(name, c.conds[name] != nil); err != nil {
		return err
	}
	if factory == nil {
		return fmt.Errorf("%w: nil factory for %q", ErrBadName, name)
	}
	c.conds[name] = factory
	return nil
}

// RegisterAction makes an action available to data as use: name. See
// RegisterCondition for the rules.
func (c *Catalog[C]) RegisterAction(name string, factory func(Args) (logic.Action[C], error)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := checkName(name, c.acts[name] != nil); err != nil {
		return err
	}
	if factory == nil {
		return fmt.Errorf("%w: nil factory for %q", ErrBadName, name)
	}
	c.acts[name] = factory
	return nil
}

// Condition builds the condition a node describes. Errors name the path to the
// offending node (rooted at "node") and are joined when several nodes are wrong.
func (c *Catalog[C]) Condition(n Node) (logic.Condition[C], error) { return c.condition(n, "node") }

// Action builds the action a node describes. See Condition for errors.
func (c *Catalog[C]) Action(n Node) (logic.Action[C], error) { return c.action(n, "node") }

// ConditionAll builds the AND of a list of condition nodes. An empty list
// yields a nil condition, which means "always true".
func (c *Catalog[C]) ConditionAll(ns []Node) (logic.Condition[C], error) {
	if len(ns) == 0 {
		return nil, nil
	}
	cs, err := c.conditions(ns, "nodes")
	if err != nil {
		return nil, err
	}
	return logic.All(cs...), nil
}

// ActionSeq builds the sequence of a list of action nodes. An empty list yields
// a nil action, which means "do nothing".
func (c *Catalog[C]) ActionSeq(ns []Node) (logic.Action[C], error) {
	if len(ns) == 0 {
		return nil, nil
	}
	as, err := c.actions(ns, "nodes")
	if err != nil {
		return nil, err
	}
	return logic.Sequence(as...), nil
}

func (c *Catalog[C]) conditions(ns []Node, path string) ([]logic.Condition[C], error) {
	out := make([]logic.Condition[C], len(ns))
	var errs []error
	for i, n := range ns {
		var err error
		if out[i], err = c.condition(n, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			errs = append(errs, err)
		}
	}
	return out, errors.Join(errs...)
}

func (c *Catalog[C]) actions(ns []Node, path string) ([]logic.Action[C], error) {
	out := make([]logic.Action[C], len(ns))
	var errs []error
	for i, n := range ns {
		var err error
		if out[i], err = c.action(n, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			errs = append(errs, err)
		}
	}
	return out, errors.Join(errs...)
}

func (c *Catalog[C]) condition(n Node, path string) (logic.Condition[C], error) {
	switch n.Use {
	case "":
		return nil, fmt.Errorf("%s: %w: missing use", path, ErrBadNode)
	case "all", "any":
		if err := shape(n, path, false, true, false); err != nil {
			return nil, err
		}
		cs, err := c.conditions(n.Of, path+".of")
		if err != nil {
			return nil, err
		}
		if n.Use == "all" {
			return logic.All(cs...), nil
		}
		return logic.Any(cs...), nil
	case "not":
		if err := shape(n, path, false, true, false); err != nil {
			return nil, err
		}
		if len(n.Of) != 1 {
			return nil, fmt.Errorf("%s: %w: not takes exactly one child in of, got %d", path, ErrBadNode, len(n.Of))
		}
		cs, err := c.conditions(n.Of, path+".of")
		if err != nil {
			return nil, err
		}
		return logic.Not(cs[0]), nil
	case "seq", "if":
		return nil, fmt.Errorf("%s: %w: %q is an action, a condition is expected here", path, ErrWrongKind, n.Use)
	}
	if err := shape(n, path, true, false, false); err != nil {
		return nil, err
	}
	c.mu.RLock()
	factory, ok := c.conds[n.Use]
	_, isAction := c.acts[n.Use]
	c.mu.RUnlock()
	if !ok {
		if isAction {
			return nil, fmt.Errorf("%s: %w: %q is an action, a condition is expected here", path, ErrWrongKind, n.Use)
		}
		return nil, fmt.Errorf("%s: %w: condition %q", path, ErrUnknownName, n.Use)
	}
	cond, err := factory(n.Args)
	if err != nil {
		return nil, fmt.Errorf("%s: %q: %w", path, n.Use, err)
	}
	return cond, nil
}

func (c *Catalog[C]) action(n Node, path string) (logic.Action[C], error) {
	switch n.Use {
	case "":
		return nil, fmt.Errorf("%s: %w: missing use", path, ErrBadNode)
	case "all", "any", "not":
		return nil, fmt.Errorf("%s: %w: %q is a condition, an action is expected here", path, ErrWrongKind, n.Use)
	case "seq":
		if err := shape(n, path, false, true, false); err != nil {
			return nil, err
		}
		as, err := c.actions(n.Of, path+".of")
		if err != nil {
			return nil, err
		}
		return logic.Sequence(as...), nil
	case "if":
		return c.ifAction(n, path)
	}
	if err := shape(n, path, true, false, false); err != nil {
		return nil, err
	}
	c.mu.RLock()
	factory, ok := c.acts[n.Use]
	_, isCondition := c.conds[n.Use]
	c.mu.RUnlock()
	if !ok {
		if isCondition {
			return nil, fmt.Errorf("%s: %w: %q is a condition, an action is expected here", path, ErrWrongKind, n.Use)
		}
		return nil, fmt.Errorf("%s: %w: action %q", path, ErrUnknownName, n.Use)
	}
	act, err := factory(n.Args)
	if err != nil {
		return nil, fmt.Errorf("%s: %q: %w", path, n.Use, err)
	}
	return act, nil
}

func (c *Catalog[C]) ifAction(n Node, path string) (logic.Action[C], error) {
	if err := shape(n, path, false, false, true); err != nil {
		return nil, err
	}
	if n.Cond == nil || n.Then == nil {
		return nil, fmt.Errorf("%s: %w: if needs both cond and then", path, ErrBadNode)
	}
	var errs []error
	cond, err := c.condition(*n.Cond, path+".cond")
	errs = append(errs, err)
	then, err := c.action(*n.Then, path+".then")
	errs = append(errs, err)
	var otherwise logic.Action[C]
	if n.Else != nil {
		otherwise, err = c.action(*n.Else, path+".else")
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return logic.If(cond, then, otherwise), nil
}

// shape rejects the fields a node of this kind must not have.
func shape(n Node, path string, args, of, ifFields bool) error {
	var extra []string
	if !args && len(n.Args) > 0 {
		extra = append(extra, "args")
	}
	if !of && len(n.Of) > 0 {
		extra = append(extra, "of")
	}
	if !ifFields && (n.Cond != nil || n.Then != nil || n.Else != nil) {
		extra = append(extra, "cond/then/else")
	}
	if len(extra) > 0 {
		return fmt.Errorf("%s: %w: %q does not take %v", path, ErrBadNode, n.Use, extra)
	}
	return nil
}
