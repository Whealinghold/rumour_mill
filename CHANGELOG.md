# Changelog

All notable changes to this project are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The project is
`v0.x` until its API has been used by at least two real consumers; minor
versions may break compatibility.

## [0.1.0] - unreleased (first tag candidate)

First complete slice of the library. Standard library only, no third-party
dependencies.

### Added
- `knowledge`: per-holder fact store with confidence, ranked source kinds,
  provenance, expiry, a pluggable merge policy, optional size cap with
  weakest-first eviction, and deterministic queries.
- `logic`: `Condition[C]` and `Action[C]` with the combinators `All`, `Any`,
  `Not`, `Sequence` and `If`.
- `rules`: a layered rule registry over a caller-defined context type, with
  caller-defined layers and scopes, priorities, stop modes (`none`, `layer`,
  `all`) and a configurable action-error policy.
- `spread`: gossip spreading with per-hop decay, confidence floors, a hop limit,
  echo suppression, per-instance filters, private facts that never travel, and
  one optional `Transform` hook.
- `infer`: an immutable inference engine with subject binding, derived facts
  that record their premises, TTLs, and bounded multi-pass chaining.
- `declare`: a catalog of named conditions and actions, JSON loading with
  strict unknown-field rejection, YAML through a caller-supplied decoder, rule,
  filter and inference loading with path-bearing errors, and the built-in
  `knows` and `add_fact` operations.
- A leak guard (`TestNoDomainVocabulary`), import-boundary and documentation
  tests, Example tests in four unrelated domains (spaceport, office, noir,
  hospital ward), and two fuzz tests for the declarative parser.

### Notes
- Not safe for concurrent use: `knowledge.Store`, `spread.Spreader` (against
  shared stores) and `infer.Engine.Run`. Safe: `rules.Registry`,
  `declare.Catalog`.
- Out of scope by design: event delivery, audience selection, a world model,
  contradiction handling, negation in inference, persistence backends.
