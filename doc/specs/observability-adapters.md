# Spec: observability adapters

Modules: `contrib/prometheus` (package `rumourprom`) and `contrib/otel` (package `rumourotel`). Status: implemented, including the gap-closing change described in section 8; sections 2 to 7 describe the target state. The core module (`rumour_mill`) stays standard-library only; the adapters are separate Go modules that depend on the core hooks, so importing the core never pulls in Prometheus or OpenTelemetry.

## 1. Purpose

Turn the core's read-only observers (`knowledge.Hooks`, `spread.Hooks`, `infer.Hooks`, `rules.Hooks`) into metrics, so a running simulation can be debugged from a dashboard: why a rumour did not arrive, why a rule did not fire, whether `MaxFacts` is evicting, whether a `Fader` is wired, whether inference is cycling or truncated, how big stores are, how long operations take. The adapters add no behaviour; observers never change an outcome and a nil observer is a no-op.

Design rule behind every metric: "never called" and "nothing to do" must look different. Every operation has a calls counter, and every silent early-out has a reason counter.

## 2. Public API

Both adapters have the same shape (`rumourprom` shown; `rumourotel` takes a `metric.Meter` instead of the registerer).

```go
// rumourprom
func New(reg prometheus.Registerer, opts ...Option) (*Metrics, error)
// rumourotel
func New(m metric.Meter, opts ...Option) (*Metrics, error)

func (m *Metrics) Spread() spread.Option    // spread.WithHooks(...)
func (m *Metrics) Infer() infer.Option      // infer.WithHooks(...)
func (m *Metrics) Store() knowledge.Option  // knowledge.WithHooks(...)
func (m *Metrics) Rules() rules.Hooks       // pass to (*rules.Registry).Observe; does NOT seed layers

// ObserveRules seeds every per-layer series from r.Layers() and r.RuleCount,
// then calls r.Observe(m.Rules()). Use it instead of registry.Observe(m.Rules()).
func (m *Metrics) ObserveRules(r RuleRegistry)

// RuleRegistry is satisfied by every *rules.Registry[C], whatever C is.
type RuleRegistry interface {
    Layers() []string
    RuleCount(layer string) int
    Observe(rules.Hooks)
}

// Options (same names and semantics in both adapters).
type Option func(*options)
func WithSourceKinds(kinds ...string) Option         // known source kinds for the `source` label; default none
func WithDurationBuckets(seconds ...float64) Option  // default DefaultDurationBuckets()
func WithHopBuckets(hops ...float64) Option          // default DefaultHopBuckets()
func WithConfidenceBuckets(c ...float64) Option      // default DefaultConfidenceBuckets()

// Defaults are functions returning a fresh slice (no package-level state).
func DefaultDurationBuckets() []float64   // [1e-6, 5e-6, 2.5e-5, 1e-4, 5e-4, 2.5e-3, 1e-2, 5e-2, 0.25, 1] seconds
func DefaultHopBuckets() []float64        // [1, 2, 3, 4, 5, 6, 8, 10, 15, 20]
func DefaultConfidenceBuckets() []float64 // [0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1]
```

`New` takes the registerer or meter the caller already has: no globals, no default registry. It returns an error when registration or instrument creation fails (for example the same registerer used twice), when a bucket list is empty, contains NaN or Inf, or is not strictly increasing, and when `WithSourceKinds` contains the empty string. Duplicate source kinds, and the literal `other`, are accepted and deduplicated. The OTel adapter uses the metrics API only; the caller owns the SDK, reader and exporter. OTel histograms use `metric.WithExplicitBucketBoundaries` with the same buckets (a caller's SDK view may still override them). `WithSourceKinds` copies its argument.

## 3. Wiring

```go
m, _ := rumourprom.New(prometheus.DefaultRegisterer,
    rumourprom.WithSourceKinds("seen", "heard", "relayed", "derived"))

now := time.Now // injected clock: the core measures nothing unless given one
sp, _ := spread.New(cfg, m.Spread(), spread.WithClock(now))
engine, _ := infer.New(icfg, irules, m.Infer(), infer.WithClock(now))
store, _ := knowledge.NewStore(kcfg, m.Store(), knowledge.WithClock(now))
registry.SetClock(now)
m.ObserveRules(registry)
```

- `m.Store()` must be wired into every store to be counted. It sees adds from every caller (`add_fact`, spread deliveries, inference), add errors, fades, prunes and (with a clock) finds.
- Durations exist only when the caller injects a clock into the core component (`WithClock`, `SetClock`). The adapters never read the wall clock themselves. Without a clock the duration series stay at zero observations and the core does no timing work.
- All options chain with the caller's own (`spread.WithHooks(mine)`, `knowledge.WithHooks(mine)`, `registry.Observe(mine)`); hooks run in the order given.
- One `Metrics` can serve many components; counters aggregate across them. Gauges are the exception, see below.
- `m.Rules()` alone still works (per-layer series then appear when first touched), but only `m.ObserveRules(registry)` pre-seeds the per-layer series and the `rules_registered` gauge. Use one `Metrics` per registry if you wire several, or accept that `rules_registered{layer}` is last-writer-wins.

## 4. Metrics

The OTel name replaces `_` separators with `.` and drops `_total`, except that `holder_type_mismatch` keeps its underscore (`rumour.holder.type_mismatch`). Labels are identical in both adapters. Types: **C** counter (monotonic), **G** gauge, **H** histogram. OTel histograms and gauges carry the units shown.

| Prometheus name | OTel name | Type | Labels | Source | Meaning |
| --- | --- | --- | --- | --- | --- |
| `rumour_spread_calls_total` | `rumour.spread.calls` | C | | `OnSpread` | `Spread` calls that ran (a teller with a store). Skipped calls are in `spread_skipped`, not here |
| `rumour_spread_skipped_total` | `rumour.spread.skipped` | C | `reason` | `Hooks.OnSkip` | `Spread` calls that returned early: `nil_teller`, `no_store` |
| `rumour_spread_listeners_skipped_total` | `rumour.spread.listeners.skipped` | C | `reason` | `Result.SkippedListeners.Each` | listeners passed to `Spread` and skipped: `nil_listener`, `no_store`. The teller listed as its own listener is not counted |
| `rumour_spread_offered_total` | `rumour.spread.offered` | C | | `Result.Offered` | (fact, listener) pairs considered |
| `rumour_spread_deliveries_total` | `rumour.spread.deliveries` | C | `outcome` | `Result.Transfers` | copies handed to a store, by what the store did |
| `rumour_spread_dropped_total` | `rumour.spread.dropped` | C | `reason` | `Result.Dropped.Each` | offers that did not become a delivery |
| `rumour_spread_withheld_total` | `rumour.spread.withheld` | C | `reason` | `Result.Withheld.Each` | facts never offered at all (`private`, `max_hops`, `below_source_confidence`, `expired`); counts facts, not pairs, and is not part of `offered` or `dropped` |
| `rumour_spread_errors_total` | `rumour.spread.errors` | C | | `OnSpread` error | `Spread` calls that returned an error |
| `rumour_spread_delivered_hops` | `rumour.spread.delivered.hops` (`{hop}`) | H | | `Transfer.Hops` | hops of copies that landed (`Outcome` is not `rejected`) |
| `rumour_spread_delivered_confidence` | `rumour.spread.delivered.confidence` (`1`) | H | | `Transfer.Confidence` | confidence of copies that landed |
| `rumour_holder_type_mismatch_total` | `rumour.holder.type_mismatch` | C | | `Result.HolderMismatches` | `Typed` filter or transform met a wrong-type holder (a wiring bug) |
| `rumour_fact_adds_total` | `rumour.fact.adds` | C | `outcome`, `source` | `Hooks.OnAdd` | store adds that returned no error, by outcome and source kind (a rejected add is still an add) |
| `rumour_fact_add_errors_total` | `rumour.fact.add.errors` | C | `reason` | `Hooks.OnAddError` | `Store.Add` calls that returned an error: `invalid`, `identity_mismatch` |
| `rumour_fact_evictions_total` | `rumour.fact.evictions` | C | | `AddResult.Evicted` | facts evicted to honour `MaxFacts` |
| `rumour_fact_pruned_total` | `rumour.fact.pruned` | C | | `PruneResult.Removed` | facts removed by `Store.Prune` |
| `rumour_prune_runs_total` | `rumour.prune.runs` | C | | `Hooks.OnPrune` | `Store.Prune` calls |
| `rumour_fade_runs_total` | `rumour.fade.runs` | C | `fader` | `Hooks.OnFade` | `Store.Fade` calls; `wired` (a Fader exists) or `none` |
| `rumour_fade_examined_total` | `rumour.fade.examined` | C | | `FadeResult.Examined` | facts the `Fader` was called on |
| `rumour_fade_changed_total` | `rumour.fade.changed` | C | | `FadeResult.Changed` | facts that survived with a different confidence |
| `rumour_fade_forgotten_total` | `rumour.fade.forgotten` | C | | `FadeResult.Forgotten` | facts removed by `Fade` |
| `rumour_fade_invalid_total` | `rumour.fade.invalid` | C | | `FadeResult.Invalid` | facts whose `Fader` returned NaN |
| `rumour_store_size` | `rumour.store.size` (`{fact}`) | G | | `AddResult.Size`, `FadeResult.Size`, `PruneResult.Size` | fact count of the store last touched (see below) |
| `rumour_store_size_max` | `rumour.store.size.max` (`{fact}`) | G | | same | high-water mark of any observed store size; never decreases |
| `rumour_infer_runs_total` | `rumour.infer.runs` | C | | `OnRun` | `Run` calls |
| `rumour_infer_passes_total` | `rumour.infer.passes` | C | | `OnRun` | passes executed |
| `rumour_infer_truncated_total` | `rumour.infer.truncated` | C | | `Result.Truncated` | `Run` calls that hit `MaxPasses` while the last pass still changed something |
| `rumour_infer_derived_total` | `rumour.infer.derived` | C | `outcome` | `Result.Derived` | derived facts, by store outcome (`inserted`, `replaced`) |
| `rumour_infer_errors_total` | `rumour.infer.errors` | C | | `OnRun` error | `Run` calls that returned an error |
| `rumour_rules_processed_total` | `rumour.rules.processed` | C | | `OnProcess` | `Process` calls |
| `rumour_rules_unmatched_total` | `rumour.rules.unmatched` | C | | `Result.Unmatched()` | `Process` calls where no layer had a candidate rule for the event type (typo'd or unregistered event type) |
| `rumour_rules_considered_total` | `rumour.rules.considered` | C | `layer` | `Result.Layers[].Candidates` | candidate rules (registered for the event type under the context's scopes), by layer |
| `rumour_rules_skipped_total` | `rumour.rules.skipped` | C | `layer` | `Result.Layers[].Skipped` | candidates whose `When` was false, by layer |
| `rumour_rules_fired_total` | `rumour.rules.fired` | C | `layer` | `Result.Fired` | rules fired, by layer |
| `rumour_rules_action_errors_total` | `rumour.rules.action.errors` | C | `layer` | `Result.Layers[].Failed` | fired rules whose `Do` returned an error, by layer (several in one `Process` count each) |
| `rumour_rules_stopped_total` | `rumour.rules.stopped` | C | `mode` | `Result.Stopped` | `Process` calls ended early (`layer`, `all`) |
| `rumour_rules_errors_total` | `rumour.rules.errors` | C | | `OnProcess` error | `Process` calls that returned an error |
| `rumour_rules_registered` | `rumour.rules.registered` (`{rule}`) | G | `layer` | `RegisterEvent.Count`, `UnregisterEvent.Count`, `RuleCount` | rules currently registered in the layer; last-writer-wins across registries sharing a `Metrics` |
| `rumour_rules_register_total` | `rumour.rules.register` | C | `result` | `Hooks.OnRegister` | `Register` calls: `ok`, `duplicate`, `invalid`, `unknown_layer` |
| `rumour_rules_unregister_total` | `rumour.rules.unregister` | C | `result` | `Hooks.OnUnregister` | `Unregister` calls: `removed`, `not_found` |
| `rumour_operation_duration_seconds` | `rumour.operation.duration` (`s`) | H | `op` | the `*Done` / `OnDuration` hooks | wall time per operation; only when a clock is injected |

### Label sets

| Label | Values | Kind |
| --- | --- | --- |
| `outcome` on `spread_deliveries`, `fact_adds` | every `knowledge.Outcome` string (`inserted`, `replaced`, `rejected`) | closed |
| `outcome` on `infer_derived` | `inserted`, `replaced` only: `rejected` cannot occur for a derived fact | closed |
| `source` on `fact_adds` | the kinds given to `WithSourceKinds`, plus `other` (every unlisted kind) | caller-defined at `New`, bounded |
| `reason` on `spread_dropped` | `filtered`, `back_to_source`, `below_min_confidence`, `transform_dropped` (`Dropped.Each`) | closed |
| `reason` on `spread_withheld` | `private`, `max_hops`, `below_source_confidence`, `expired` (`Withheld.Each`) | closed |
| `reason` on `spread_skipped` | `nil_teller`, `no_store` (`spread.SkipReasons()`) | closed |
| `reason` on `spread_listeners_skipped` | `nil_listener`, `no_store` (`SkippedListeners.Each`) | closed |
| `reason` on `fact_add_errors` | `invalid`, `identity_mismatch` (`knowledge.AddErrorReasons()`) | closed |
| `fader` on `fade_runs` | `wired`, `none` | closed |
| `mode` on `rules_stopped` | `layer`, `all` | closed |
| `result` on `rules_register` | `ok`, `duplicate`, `invalid`, `unknown_layer` | closed |
| `result` on `rules_unregister` | `removed`, `not_found` | closed |
| `op` on `operation_duration_seconds` | `spread`, `infer_run`, `fade`, `prune`, `rules_process`, `store_find` | closed |
| `layer` on every `rules_*` metric that has it | layer names of the registry (`Registry.Layers()`) | caller-defined, fixed at wiring |

Labels are never fact IDs, subjects, rule IDs or event types, so cardinality is bounded by the closed sets, the number of layers and the number of source kinds the caller listed.

`source` mapping: the adapter looks up `Fact.Source` in the set given to `WithSourceKinds`; a hit uses it verbatim, anything else (including the empty string and `other` itself when unlisted) is `other`. With no `WithSourceKinds` option every add carries `source="other"`; sum over `source` to get the previous totals.

### Store size gauges

`knowledge.Store` is not concurrency-safe and the adapters never poll it. Each `OnAdd`, `OnFade` and `OnPrune` carries the store's size after the operation (`AddResult.Size`, `FadeResult.Size`, `PruneResult.Size`); the adapter sets `rumour_store_size` to it and raises `rumour_store_size_max` if it is higher. With one store `rumour_store_size` is that store's exact size as of its last add, fade or prune (an add that errors does not change it). With several stores wired into one `Metrics`, `rumour_store_size` is the size of whichever store was touched last, a sampling of the fleet rather than a sum; read `rumour_store_size_max` for the largest store ever seen, and compare it with `MaxFacts`. Keep a separate `Metrics` (separate registerer or meter) per store if per-store size matters. `rumour_store_size_max` is updated with a compare-and-swap or lock so concurrent stores never lower it.

### Histograms

- `rumour_spread_delivered_hops` and `rumour_spread_delivered_confidence` observe one value per `Transfer` whose `Outcome` is `inserted` or `replaced`; a `rejected` copy did not land and is not observed. Default buckets: hops `[1, 2, 3, 4, 5, 6, 8, 10, 15, 20]`, confidence `[0.1 ... 1.0]` in steps of 0.1. Overridable with `WithHopBuckets` and `WithConfidenceBuckets`.
- `rumour_operation_duration_seconds{op}` observes seconds. Default buckets `[1e-6, 5e-6, 2.5e-5, 1e-4, 5e-4, 2.5e-3, 1e-2, 5e-2, 0.25, 1]`, overridable with `WithDurationBuckets`. Ops: `spread` (`OnSpreadDone`), `infer_run` (`OnRunDone`), `fade`, `prune`, `store_find` (`knowledge.Hooks.OnDuration` with `OpFade`, `OpPrune`, `OpFind`), `rules_process` (`OnProcessDone`). `store_find` fires for every `Find` call on the store, including the ones `infer.Run` makes; it can be a high-volume series, so keep the clock off if that matters. Negative durations (a non-monotonic injected clock) are observed as 0.

### Zero-seeding

At `New`, every series of every closed-set label is created at 0 (the sets above are enumerated from the core: `Dropped{}.Each`, `Withheld{}.Each`, `SkippedListeners{}.Each`, `spread.SkipReasons()`, `knowledge.AddErrorReasons()`, the outcome list), so `rate()` and alerts work from the first scrape and an absent series never has to be read as "zero". `fact_adds` is seeded for every outcome times every source (listed kinds plus `other`). The two store-size gauges start at 0. In Prometheus the histogram `op` series are seeded too (an empty histogram per op). `m.ObserveRules(registry)` seeds the caller-defined `layer` series (`considered`, `skipped`, `fired`, `action_errors`, `registered`) for every name in `Registry.Layers()`, so "layer never fires" is `rate(rumour_rules_fired_total{layer="x"}[1h]) == 0`, not an absent series. Layers are fixed at `NewRegistry`, so nothing appears later. `m.Rules()` without `ObserveRules` leaves layer series unseeded (they appear on first use). In OTel the seeding is `Add(ctx, 0, attrs)`; the SDK exports the series from then on. OTel histograms cannot be seeded without recording a fake observation and are not: an OTel histogram appears after its first observation.

### Debugging interpretation

- `holder_type_mismatch` non-zero: a `Typed` filter is blocking everything (also counted in `dropped{reason="filtered"}`); fix the wiring.
- `fade_runs{fader="none"}` non-zero (or `fade_runs` all `none`): `Fade` is being called with no `Fader` wired (no positive half-life); `fade_examined` is then 0 by construction. `fade_runs{fader="wired"}` rising with `fade_examined` flat: the store is empty or everything is expired. `fade_runs` flat at 0: `Fade` is never called. `fade_invalid` non-zero: the `Fader` returns NaN.
- `prune_runs` flat while facts expire: `Prune` is never called. `prune_runs` rising with `fact_pruned` flat: nothing expired.
- `spread_calls` flat: `Spread` is never called. `spread_skipped` rising: the caller passes a nil teller or one without a store. `spread_listeners_skipped{reason="no_store"}`: listeners that can never receive; `nil_listener`: holes in the listener slice.
- `spread_withheld` explains facts that never travel: `private`, `max_hops`, `below_source_confidence` (teller is not sure enough), `expired` (dead facts awaiting `Prune`). `spread_dropped` explains offers that did not land. `spread_delivered_hops` and `spread_delivered_confidence` show how far and how faintly rumours travel; a pile in the lowest confidence bucket means `decay_factor` or `min_confidence` is tuned too tight.
- `rules_unmatched` rising: events reach `Process` that no rule is registered for (typo'd event type, or the rules never loaded; confirm with `rules_registered`). `rules_considered{layer}` rising while `rules_fired{layer}` is flat and `rules_skipped{layer}` tracks `considered`: rules exist for the event but their `When` is always false. `considered` flat for a layer: no rule of that layer matches the context's scope keys. `rules_fired{layer}` rate 0: the layer never fires. `rules_action_errors{layer}` counts every failing action, even several in one `Process` (`rules_errors` counts calls).
- `rules_register{result="duplicate"|"invalid"|"unknown_layer"}` non-zero: data-loading bugs. `rules_registered{layer}` falling to 0 means everything was unregistered.
- `infer_truncated` non-zero: `MaxPasses` is too small for the rule chain (or a rule cycle keeps replacing facts); raise `max_passes` or check for cycles. With `max_passes = 1` the engine never reports truncation, because chaining is off by design. `infer_passes / infer_runs` creeping up means chaining or a cycle.
- `fact_add_errors{reason="invalid"}`: a caller (an `add_fact` action, a transform, a rule's product) builds facts with an empty type or source, NaN confidence or a U+001F in an identity field. `identity_mismatch`: two different claims share an explicit fact ID. Both are returned to the caller and, now, logged and counted.
- `fact_adds{source}` shows which kinds of knowledge are being added; a large `other` means `WithSourceKinds` is missing a kind.
- `store_size_max` approaching the configured `MaxFacts` together with rising `fact_evictions`: the cap is biting.
- Durations: a slow dimension is visible only when a clock is injected; `histogram_quantile(0.99, rate(rumour_operation_duration_seconds_bucket{op="spread"}[5m]))`.

## 5. Not observed

- `declare` loading runs at startup and returns its errors; no counter. (Rule registration made through it is counted by `rules_register`.)
- A failed `rules.Register` is still unlogged (the error is returned to the caller) but is counted.
- The teller listed as its own listener in `Spread` is skipped silently and uncounted; it is expected and harmless.
- Per-rule, per-fact, per-subject and per-event-type series: unbounded cardinality. Use the logs (rule IDs appear in the `rules: processed` Debug line and per-rule counts in the `infer run` Debug line).
- Store sizes are not summed across stores (see above).

## 6. Versioning

Each adapter has its own `go.mod` and is tagged separately from the root: `contrib/prometheus/vX.Y.Z` and `contrib/otel/vX.Y.Z`. Until `just release-adapters vX.Y.Z` has been run and the printed tags pushed, the adapters are not published; consumers need `replace` lines for both `github.com/Whealinghold/rumour_mill` and the adapter in their own `go.mod` (the adapter's own `replace` does not carry over). `release-adapters` runs the race tests and `go vet` on all modules, pins the adapters' `require` to the root version atomically (both `go.mod` files or neither; a failure restores them), keeps the in-repo `replace` for development, and prints the tag and push commands. It never tags or pushes.

## 7. Error cases and concurrency

`New` errors: registration failure (Prometheus) or instrument creation failure (OTel); invalid bucket list; empty source kind. The adapters' hooks are synchronous, must not panic, and are safe for concurrent use to the extent their backing instruments are (Prometheus collectors and OTel instruments are; the adapters' own state, the size high-water mark, is atomic). `ObserveRules` is safe to call once per registry at wiring time.

## 8. Core hooks the adapters consume (the gap-closing change)

The authoritative signatures are in [knowledge](knowledge.md), [rules](rules.md) and [spreading-and-inference](spreading-and-inference.md); this table is the index.

| Package | New or changed |
| --- | --- |
| `knowledge` | `Store.Len()`; `AddResult.Size`; `FadeResult.Size`, `FadeResult.Wired`; `PruneResult`; `Hooks.OnPrune` takes `PruneResult`; `Hooks.OnFade` also fires with no Fader; `Hooks.OnAddError(f, reason, err)`; `AddErrorInvalid`, `AddErrorIdentityMismatch`, `AddErrorReasons()`; `WithClock`; `Hooks.OnDuration(op, d)`; `OpFade`, `OpPrune`, `OpFind` |
| `rules` | `Registry.Layers()`, `Registry.RuleCount(layer)`, `Registry.SetClock`; `Result.Layers` (`LayerStats`), `Result.Unmatched()`; `Fired.Failed`; `Hooks.OnRegister`, `OnUnregister`, `OnProcessDone`; `RegisterEvent`, `UnregisterEvent` |
| `spread` | `Result.SkippedListeners` (`SkippedListeners`, with `Each`); `Withheld.BelowSourceConfidence`, `Withheld.Expired`; `Transfer.Hops`; `Hooks.OnSkip`, `Hooks.OnSpreadDone`; `SkipNilTeller`, `SkipNoStore`, `SkipReasons()`; `WithClock` |
| `infer` | `Result.Truncated`; `Hooks.OnRunDone`; `WithClock`; richer Debug and a Warn on truncation |

Back-compat: every addition is a new field, method or option; zero values mean "not observed". The one exception is `knowledge.Hooks.OnPrune`, whose parameter changes from `int` to `PruneResult` (the API is unreleased); the existing hook tests are updated in the same change.
