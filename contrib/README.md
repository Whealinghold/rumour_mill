# Observability adapters

> **Not published yet.** The adapter modules are not available as tagged versions until the `just release-adapters` step has been done and its tags pushed. Until then consumers need `replace` lines (see "Using from another module"). Full contract: [observability-adapters spec](../doc/specs/observability-adapters.md).

The core library has no dependencies beyond the standard library (loggers are `log/slog`). These adapters are separate Go modules, so importing `rumour_mill` never pulls in Prometheus or OpenTelemetry. Each takes the registry or meter you already have (no globals).

- [`prometheus`](prometheus) (`rumourprom`): `m, err := rumourprom.New(prometheus.DefaultRegisterer)`.
- [`otel`](otel) (`rumourotel`): `m, err := rumourotel.New(meterProvider.Meter("rumour_mill"))`. API only; you own the SDK and exporter.

## Wiring

```go
sp, _ := spread.New(cfg, m.Spread())              // spread option
engine, _ := infer.New(icfg, irules, m.Infer())   // infer option
store, _ := knowledge.NewStore(kcfg, m.Store())   // store option: sees add_fact, spread deliveries, infer, fade and prune
m.ObserveRules(registry)                          // rules hooks; also pre-seeds every layer's series
```

Options on `New`: `WithSourceKinds("seen", "heard", ...)` (the known values of the `source` label on `fact_adds`; anything else is `other`), `WithDurationBuckets`, `WithHopBuckets`, `WithConfidenceBuckets` (defaults: `DefaultDurationBuckets()`, `DefaultHopBuckets()`, `DefaultConfidenceBuckets()`). Durations appear only if you also give the core components a clock: `spread.WithClock(time.Now)`, `infer.WithClock`, `knowledge.WithClock`, `registry.SetClock`. `registry.Observe(m.Rules())` still works but leaves the per-layer series unseeded.

Wire `m.Store()` into every store you want counted. Options are repeatable and chain, so add your own hook or logger alongside (`spread.WithHooks(mine)`, `knowledge.WithHooks(mine)`, `registry.Observe(mine)`).

## Metrics

| Prometheus name | OTel name | Labels | Debugging use |
| --- | --- | --- | --- |
| `rumour_spread_offered_total` | `rumour.spread.offered` | | pairs considered; the denominator for deliveries and drops |
| `rumour_spread_deliveries_total` | `rumour.spread.deliveries` | `outcome` | `rejected` high means listeners already know better |
| `rumour_spread_dropped_total` | `rumour.spread.dropped` | `reason` | why offers did not arrive: filter, decay, transform |
| `rumour_spread_withheld_total` | `rumour.spread.withheld` | `reason` | facts never offered (`private`, `max_hops`, `below_source_confidence`, `expired`); counts facts, not pairs, and is not in `offered` or `dropped` |
| `rumour_spread_calls_total` | `rumour.spread.calls` | | `Spread` calls that ran |
| `rumour_spread_skipped_total` | `rumour.spread.skipped` | `reason` | `Spread` calls that returned early: `nil_teller`, `no_store` |
| `rumour_spread_listeners_skipped_total` | `rumour.spread.listeners.skipped` | `reason` | skipped listeners: `nil_listener`, `no_store` (the teller as its own listener is not counted) |
| `rumour_spread_delivered_hops` | `rumour.spread.delivered.hops` | | histogram: hops of copies that landed (not `rejected`) |
| `rumour_spread_delivered_confidence` | `rumour.spread.delivered.confidence` | | histogram: confidence of copies that landed |
| `rumour_spread_errors_total` | `rumour.spread.errors` | | `Spread` calls that returned an error (store failures) |
| `rumour_holder_type_mismatch_total` | `rumour.holder.type_mismatch` | | non-zero is a wiring bug in a `Typed` filter or transform (OTel keeps the underscore) |
| `rumour_fact_adds_total` | `rumour.fact.adds` | `outcome`, `source` | store adds that returned no error, by outcome (a rejected add is still an add) and source kind (`WithSourceKinds` plus `other`): add_fact, spread, infer |
| `rumour_fact_add_errors_total` | `rumour.fact.add.errors` | `reason` | `Store.Add` errors: `invalid`, `identity_mismatch` |
| `rumour_store_size` | `rumour.store.size` | | gauge: size of the store last touched by an add, fade or prune |
| `rumour_store_size_max` | `rumour.store.size.max` | | gauge: largest store size ever observed; compare with `MaxFacts` |
| `rumour_prune_runs_total` | `rumour.prune.runs` | | `Store.Prune` calls; flat means Prune is never called |
| `rumour_fade_runs_total` | `rumour.fade.runs` | `fader` | `Store.Fade` calls, `wired` or `none` (no Fader: `examined` is 0 by construction) |
| `rumour_fact_evictions_total` | `rumour.fact.evictions` | | non-zero means `MaxFacts` is dropping facts |
| `rumour_fact_pruned_total` | `rumour.fact.pruned` | | facts removed by `Store.Prune`; flat while facts expire means Prune is not being called |
| `rumour_fade_examined_total` | `rumour.fade.examined` | | zero while the store is non-empty means no fader is wired |
| `rumour_fade_changed_total` | `rumour.fade.changed` | | confidence being lowered |
| `rumour_fade_forgotten_total` | `rumour.fade.forgotten` | | facts forgotten below `forget_below` |
| `rumour_fade_invalid_total` | `rumour.fade.invalid` | | non-zero means a `Fader` returns NaN |
| `rumour_infer_runs_total` | `rumour.infer.runs` | | `Run` calls |
| `rumour_infer_passes_total` | `rumour.infer.passes` | | passes per run creeping up means chaining or a rule cycle |
| `rumour_infer_truncated_total` | `rumour.infer.truncated` | | runs that hit `MaxPasses` while the last pass still changed something (never with `max_passes = 1`) |
| `rumour_infer_derived_total` | `rumour.infer.derived` | `outcome` | `inserted` / `replaced` conclusions only (a derived fact is never `rejected`) |
| `rumour_infer_errors_total` | `rumour.infer.errors` | | `Run` calls that returned an error |
| `rumour_rules_processed_total` | `rumour.rules.processed` | | `Process` calls |
| `rumour_rules_unmatched_total` | `rumour.rules.unmatched` | | events with no candidate rule in any layer (typo'd event type) |
| `rumour_rules_considered_total` | `rumour.rules.considered` | `layer` | candidate rules per layer |
| `rumour_rules_skipped_total` | `rumour.rules.skipped` | `layer` | candidates whose `When` was false |
| `rumour_rules_fired_total` | `rumour.rules.fired` | `layer` | which layer's rules actually fire |
| `rumour_rules_action_errors_total` | `rumour.rules.action.errors` | `layer` | fired rules whose action failed (each counted, unlike `rules_errors`) |
| `rumour_rules_registered` | `rumour.rules.registered` | `layer` | gauge: rules currently registered |
| `rumour_rules_register_total` | `rumour.rules.register` | `result` | `ok`, `duplicate`, `invalid`, `unknown_layer` |
| `rumour_rules_unregister_total` | `rumour.rules.unregister` | `result` | `removed`, `not_found` |
| `rumour_operation_duration_seconds` | `rumour.operation.duration` | `op` | histogram, seconds; needs a clock injected into the core (`spread`, `infer_run`, `fade`, `prune`, `rules_process`, `store_find`) |
| `rumour_rules_stopped_total` | `rumour.rules.stopped` | `mode` | `layer` or `all`: stop rules cutting processing short |
| `rumour_rules_errors_total` | `rumour.rules.errors` | | `Process` calls that returned an action error |

Labels are never fact IDs, subjects, rule IDs or event types, so cardinality is bounded. Closed-set labels (`outcome`, `reason`, `mode`, `fader`, `result`, `op`) are zero-seeded at `New`, `fact_adds` for every outcome times every listed source, so every series exists at 0 from the first scrape and `rate()` and alerts work before the first event. `layer` series are seeded by `m.ObserveRules(registry)` from the registry's layer names, so "layer never fires" is an alertable zero rate rather than an absent series. OTel histograms are not seeded (that would fake an observation). With several stores in one `Metrics`, `rumour_store_size` is whichever store was touched last; use `rumour_store_size_max` or one `Metrics` per store.

A wrong-type holder in a `Typed` filter increments `rumour_holder_type_mismatch_total` and also `dropped{reason="filtered"}`; a high `filtered` rate with a non-zero mismatch counter is a wiring bug, not policy.

`rumour_fact_evictions_total` counts evicted facts. With `outcome="rejected"` and an eviction, the new fact itself was the weakest and was dropped.

### What is not observed, and why

- `declare` loading runs at startup and returns its errors (the rules it registers are counted by `rules_register`).
- The teller listed among its own listeners is skipped silently and is not counted.
- Per-rule, per-fact and per-event-type series would be unbounded; rule IDs appear in the Debug logs instead.
- Store sizes are not summed across stores.

A nil teller (or one without a store) still makes `Spread` return early without calling `OnSpread`, but it now moves `spread_skipped{reason}`.

## Loggers

Each component logs through `log/slog` and discards by default: `spread.WithLogger(l)`, `infer.WithLogger(l)`, `knowledge.WithLogger(l)`, `registry.SetLogger(l)`; a nil logger is ignored.

| Component | Debug | Warn |
| --- | --- | --- |
| spread | one summary per `Spread` (includes withheld and skipped listeners) | once per call on store errors (teller, error count); once per call when `Transform` dropped deliveries; once per call when holders mismatched; once per call when listeners were skipped (nil or no store); a nil or store-less teller |
| infer | one summary per `Run` (passes, derived, truncated, per-rule derived counts) | `Run` returned an error; `Run` was truncated at `MaxPasses` |
| knowledge | one summary per `Fade` (including `wired=false` when no Fader); one per `Prune` (removed count, size) | an `Add` evicted facts (names the ADDED fact, not the evicted ones); an `Add` failed (`ErrIdentityMismatch`, or an invalid fact: type, source, error); a `Fade` found invalid Fader results (once per call, count and up to 5 IDs) |
| rules | one summary per `Process` (fired rule IDs, per-layer considered/skipped/failed, unmatched); each `Register` and `Unregister` (layer, scope, rule, ok) | `Process` returned an error |

A failed `Register` is not logged (the error is returned) but is counted in `rules_register{result}`.

Logger and metrics combine freely and independently: metrics tell you that something is off (`fact_evictions` rising, `spread_errors` non-zero), and the Warn line, at Debug level the surrounding summaries, says which fact, teller or event. Run at Warn in production and drop to Debug while investigating.

## Using from another module

The adapters are tagged separately from the root, as `contrib/prometheus/vX.Y.Z` and `contrib/otel/vX.Y.Z`. Once published:

```
go get github.com/Whealinghold/rumour_mill/contrib/prometheus@vX.Y.Z
```

The root module resolves to the version the adapter pins. Before a tag exists, add `replace` lines for both `github.com/Whealinghold/rumour_mill` and the adapter to your `go.mod` (the adapter's own `replace` does not carry over), then run `go mod tidy`.

Maintainers: `just release-adapters vX.Y.Z` verifies, pins the adapters to that root version and prints the tag and push commands. It never tags or pushes.
