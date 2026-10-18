// Package declare loads rules, spreading filters, inference rules and config
// from data (JSON from the standard library, YAML through a decoder the caller
// supplies) and binds them to named conditions and actions the caller
// registers.
//
// It is the only package that knows about data formats. The core packages
// (knowledge, logic, rules, spread, infer) never import it, so a program that
// builds everything in Go does not need it. Nothing here is global: a Catalog
// is an instance, and no name is registered unless the caller registers it.
package declare

import "errors"

// Errors returned by this package. Check with errors.Is. Errors that point at
// a place in a document carry a path such as rules[3].when.of[1].
var (
	ErrBadArgs            = errors.New("declare: bad arguments")
	ErrBadNode            = errors.New("declare: malformed node")
	ErrBadName            = errors.New("declare: bad name")
	ErrBadBindings        = errors.New("declare: incomplete bindings")
	ErrDuplicateName      = errors.New("declare: name already registered")
	ErrUnknownName        = errors.New("declare: unknown name")
	ErrWrongKind          = errors.New("declare: wrong kind of name")
	ErrNoStore            = errors.New("declare: no knowledge store for this context")
	ErrInvalidConfig      = errors.New("declare: invalid config")
	ErrUnsupportedVersion = errors.New("declare: unsupported document version")
)
