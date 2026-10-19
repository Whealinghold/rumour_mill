# Changelog

All notable changes to this project are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The project is
`v0.x` until its API has been used by at least two real consumers; minor
versions may break compatibility.

## [Unreleased]

### Changed
- `spread` now resets `FadedAt` to 0 on the listener's copy of a delivered fact; the teller's fact keeps its own.
- Breaking: `spread.Spreader.Transform` now has the named type
  `spread.Transform`, `func(e Encounter, delivered knowledge.Fact) knowledge.Fact`.
  Migrate with a one-line wrapper: `func(_ spread.Encounter, f knowledge.Fact) knowledge.Fact { return old(f) }`.
- Unreleased-API change: `Bindings.OnAdd`, `Bindings.Logger` and `declare.ChainOnAdd` (and the adapters' `Mismatch()` and `OnAdd`) were removed before any release. Observe adds at the store with `knowledge.WithHooks` / `knowledge.WithLogger`, which also sees spread deliveries and inference; mismatches come from `spread.Result.HolderMismatches`.

### Added
- Observability, all optional and no-op by default; core stays standard-library only (`log/slog` for logging). Every hook is synchronous and read-only, and repeatable options chain (run in the order given, nil ignored):
  - `knowledge.Hooks` (`OnAdd(f, r)` after every successful `Store.Add` from any caller: `add_fact`, spread delivery, inference; `OnFade(r)` after every `Fade`; see the gap closure below for later changes), `knowledge.WithHooks`, `knowledge.WithLogger` (Warn when an Add evicts facts, Debug summary per Fade).
  - `spread.Hooks.OnSpread(Result, error)` with the joined store errors, `spread.WithHooks`, `spread.WithLogger` (Debug summary per call, one Warn per call when holders mismatched), `spread.Result.HolderMismatches`, and `spread.OnMismatch` (a `TypedOption` for `Typed` and `TypedTransform`).
  - `infer.New(cfg, rules, opts...)` now takes options: `infer.Option`, `infer.Hooks.OnRun(Result, error)`, `infer.WithHooks`, `infer.WithLogger`.
  - `rules.Hooks.OnProcess(Result, error)`, `(*Registry).Observe(Hooks)` (safe at any time, applies from the next `Process`) and `(*Registry).SetLogger`.
  - `spread.Dropped.Each` visits every drop reason, for adapters.
  - `knowledge.Hooks.OnPrune` (superseded below by `PruneResult`) runs on every `Store.Prune`, including with 0. `spread.Result.Withheld` (`Private`, `MaxHops`, with `Each`) counts facts the teller held but never offered; it is not part of `Offered` or `Dropped`, so the `Offered` identity is unchanged.
  - More log lines: Prune Debug; Fade Warn (once per call) when the Fader returned NaN; `Store.Add` Warn on `ErrIdentityMismatch`; `Spread` Warn on store errors and on transform drops, and for a nil or store-less teller (which still returns early without calling `OnSpread`, as there is no `Result`; `Hooks.OnSkip` reports it, see below); `Registry.Register` and `Unregister` Debug.
- Observability gap closure (all additive unless noted; zero values mean "not observed"):
  - `knowledge`: `Store.Len()`; `AddResult.Size`; `FadeResult.Wired` and `.Size`; `PruneResult`; `Hooks.OnAddError(f, reason, err)` with `AddErrorInvalid`, `AddErrorIdentityMismatch` and `AddErrorReasons()` (invalid facts are now logged at Warn); `Hooks.OnFade` now also fires for a store with no Fader (`Wired == false`); `WithClock` and `Hooks.OnDuration(op, d)` (`OpFind`, `OpFade`, `OpPrune`). Breaking, unreleased API: `Hooks.OnPrune` now takes a `PruneResult` instead of an `int`.
  - `rules`: `Result.Layers` (`LayerStats`: candidates, skipped, failed per layer), `Result.Unmatched()`, `Fired.Failed`, `Registry.Layers()`, `Registry.RuleCount(layer)`, `Registry.SetClock`, `Hooks.OnProcessDone`, `Hooks.OnRegister` / `OnUnregister` (`RegisterEvent`, `UnregisterEvent`); the Debug line lists the fired rule IDs. Note: positional `Fired{...}` literals need the new field.
  - `spread`: `Result.SkippedListeners` (with `Each`), `Withheld.BelowSourceConfidence` and `Withheld.Expired`, `Transfer.Hops`, `Hooks.OnSkip` with `SkipNilTeller`, `SkipNoStore`, `SkipReasons()`, `Hooks.OnSpreadDone`, `WithClock`; the `Offered` identity is unchanged.
  - `infer`: `Result.Truncated` (plus a Warn), `Hooks.OnRunDone`, `WithClock`, per-rule derived counts in the Debug line.
  - Adapters: `New(..., opts...)` with `WithSourceKinds`, `WithDurationBuckets`, `WithHopBuckets`, `WithConfidenceBuckets`; `m.ObserveRules(registry)` seeds per-layer series; new metrics for store size, add errors, fade/prune/spread call counts, skipped spreads and listeners, truncation, rule considered/skipped/action errors/unmatched/registered/register/unregister, delivered hops and confidence histograms and operation durations; `fact_adds` gains a `source` label. See `doc/specs/observability-adapters.md`.
- Separate adapter modules `contrib/prometheus` (`rumourprom`) and `contrib/otel` (`rumourotel`): `New(registerer | meter)` then `m.Spread()`, `m.Infer()`, `m.Store()` and `registry.Observe(m.Rules())`. Closed-set label values are zero-seeded. New metrics `rumour_fact_pruned_total` (`rumour.fact.pruned`) and `rumour_spread_withheld_total{reason}` (`rumour.spread.withheld`); `infer_derived{outcome}` is seeded with `inserted` and `replaced` only. See `contrib/README.md`. Spec: `doc/specs/observability-adapters.md`.
- `just release-adapters VERSION`: checks, pins the adapters to VERSION and prints (never runs) the tag and push commands.
- `spread.Typed`, `spread.TypedEncounter` and `spread.TypedTransform`: typed spreading filters and transforms, no hand type assertion (a mismatched holder is blocked / passed through).
- `declare.Attributes`, `declare.AttributeNames`, `declare.DefaultAttributeNames` and `(*Catalog).RegisterAttributeOps`: built-in `attr_equals`, `attr_in`, `attr_same` conditions over a caller-supplied lookup.
- `declare.ArgNames`, `declare.DefaultArgNames`, `Bindings.Discriminators` and `Bindings.ArgNames`; `add_fact` args `discriminator` and `discriminator_from`.
- `spread.Result.Offered` and `spread.Result.Dropped` (`Filtered`, `BackToSource`,
  `BelowMinConfidence`, `TransformDropped`) explain offers that did not arrive.
- `knowledge.FadeResult.Examined` and `Invalid` expose a Fade that is not wired
  up or a Fader returning NaN.
- `knowledge.Fact.FadedAt` (settle point of the last fade; hand-written
  serialisers must persist it), `knowledge.Fader`, `knowledge.FadeResult`,
  `knowledge.WithFader`, `(*Store).Fade(now)`, and `knowledge.Config.Fade`
  (`half_lives`, `default_half_life`, `forget_below`) with a built-in exponential
  half-life policy. Off by default; also loadable through `declare`.

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
