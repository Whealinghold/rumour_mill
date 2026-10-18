# Architecture rules

Settled. Do not re-litigate.

## The prime directive
This library must be usable by any game or simulation with any setting. It contains **no domain facts**. If a change would make the library mention a specific world, culture, profession, item, location, or event, it is wrong; put it in the caller's config instead.

## Rules
1. **No hardcoded vocabulary.** Fact types, event types, source kinds, entity kinds, and condition names are caller-defined open types (string-based or generic parameters), never library enums of domain terms.
2. **No magic numbers.** Every tunable (spread decay, minimum confidence, query floor, source-strength ranking, priorities, limits) is a field in a config struct with a documented default. Defaults live in one place (`DefaultConfig()` or equivalent) and are overridable.
3. **No world model.** The library never assumes entities have factions, rooms, zones, players, calendars, or economies. Anything about the host world is reached through small caller-implemented interfaces or caller-supplied functions.
4. **Time is abstract.** Use a caller-supplied time type or an `int64` tick. No calendars, seasons, or clock logic.
5. **No global state.** No package-level registries, `init()` registration, or singletons. Everything is an instance configured at construction (functional options or config struct).
6. **Policy is injected.** Spreading filters, source-strength ranking, confidence degradation, and inference rules are supplied by the caller. The library ships generic combinators (`All`, `Any`, `Not`, `Sequence`, `If`) and hooks, not setting-specific rules.
7. **Declarative where sensible.** Rules, inference rules, and config must be loadable from data (YAML/JSON) via caller-registered named conditions and actions, so behaviour changes without recompiling.
8. **Storage is a concrete in-memory type.** Introduce a persistence interface only when a real second backend or a consumer-side test seam exists. Event delivery is the caller's; the library ships no transport. No database, network, or scheduler code.
9. **Small public API.** Export the minimum. Internal helpers go in `internal/`. Keep interfaces small and consumer-shaped.
10. **Concurrency is documented.** Each exported type states whether it is safe for concurrent use. Guard shared state with a mutex where claimed.
11. **Determinism.** Given the same inputs, config, and injected RNG/clock, results are identical. Randomness is always injected.
12. **Errors are values.** Return errors; no panics in library code on bad input.

## Out of scope
Game schedulers, DB persistence, dialogue, behaviour gates tied to a game, combat, economy.

## Leak guard
A test (`TestNoDomainVocabulary`) scans non-test source and docs for banned terms (the originating game's names and settings, e.g. tyrian, aldamar, barrow, dread, merchant, stanford) and fails if any appear. It must exclude `doc/design/reference/`, which is intentionally game-flavoured read-only reference. Add terms to its list if new leaks are found.

## Idiomatic Go

Settled. Priority order when rules pull apart: simplicity, testability, then flexibility.

1. **Usage first.** Before implementing a package, write its `Example_` tests (and the README snippet) and adjust the API until the call sites read well. Optimise for usability, readability, flexibility, testability, in that order.
2. **Smallest exported surface.** When in doubt leave it out: you can export later, never unexport. Helpers go in `internal/`.
3. **Accept interfaces, return concrete structs.** Define interfaces where they are consumed, 1-3 methods. No interface without a real second implementation or a genuine test seam; no interface just to mock. (Rule 8 applies this to storage.)
4. **Generics only where they earn it.** Type parameters are used for the caller-defined context `C` in conditions, actions and rules. The knowledge store, spreading and inference are concrete. Add a type parameter when duplication forces it, not up front.
5. **Zero values useful where possible.** Config is a plain exported struct (data-loadable, checked by `Validate()`) with `DefaultConfig()`. Functional options only for injected dependencies (filters, merge policy, clock/tick source, RNG), never for tunables.
6. **Errors.** Return errors, wrap with `%w`, expose sentinels or typed errors usable with `errors.Is/As`, never panic on bad input. `context.Context` only on interfaces that could do I/O, not on pure in-memory logic.
7. **Small packages, one purpose, no import cycles.** The declarative loader (decoder, catalog, document) lives in its own `declare` package so the core packages are usable without it. Core packages import only the standard library and each other.
8. **Concurrency.** Document it on every exported type. Simple types (store, spreader, engine) are "not safe for concurrent use; wrap with your own lock". Only `rules.Registry` and `declare.Catalog` carry locks.
9. **Testing.** Table-driven, fakes over mocks, hermetic, seeded `*rand.Rand` and injected tick source, `Example` functions that double as docs, a fuzz test for the declarative parser. `go vet` clean, `go test -race` clean.
10. **Docs.** Package doc comment on every package, doc comment on every exported identifier, README with a quick start in a neutral domain.
11. **Versioning.** `v0.x` until the API has been used by at least two real consumers.
