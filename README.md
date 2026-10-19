# rumour_mill

A reusable, configurable Go library for NPC rumour propagation: layered event rules, per-entity knowledge with provenance and confidence, gossip spreading with decay, and fact inference. It contains no game or setting: every fact type, source kind, tuning constant and rule is supplied by the caller.

**Status: v0.x, pre-release.** All packages exist: `knowledge`, `logic`, `rules`, `spread`, `infer` and the optional `declare` loader. See [CHANGELOG.md](CHANGELOG.md) for the v0.1.0 candidate. The API may change until at least two real consumers have used it. Design docs live in [`doc/`](doc/README.md).

## Quick start

An office-gossip sim: what one employee believes, and how a second-hand telling interacts with a first-hand one.

```go
cfg := knowledge.DefaultConfig()
cfg.SourceRanks = map[string]int{"announcement": 3, "overheard": 2, "grapevine": 2, "guess": 1}
store, err := knowledge.NewStore(cfg)
if err != nil {
	log.Fatal(err)
}

expiry := knowledge.Tick(220) // a tick is whatever your simulation says it is
store.Add(knowledge.Fact{
	Type: "promotion_pending", Subject: "dana-from-accounts",
	Confidence: 0.5, Source: "grapevine", Observed: 100, Expiry: &expiry,
})

// A stronger telling of the same claim at the same rank replaces it;
// a higher-ranked source (an announcement) always wins.
store.Add(knowledge.Fact{
	Type: "promotion_pending", Subject: "dana-from-accounts",
	Confidence: 0.7, Source: "grapevine", Observed: 120, Expiry: &expiry,
})

for _, f := range store.Find(knowledge.Query{Type: "promotion_pending"}, 130) {
	fmt.Println(f.Subject, f.Confidence, f.Source) // dana-from-accounts 0.7 grapevine
}
```

Fact types, source kinds and ranks are plain strings and config you choose. See [`knowledge/example_test.go`](knowledge/example_test.go) for a spaceport trading hub as well.

### Rules

The `rules` package runs layered rules over a context type you define (`C`); it does not depend on `knowledge`. A noir-flavoured sketch:

```go
type Witness struct{ Name, Precinct string }

reg, _ := rules.NewRegistry(rules.DefaultConfig(),
	rules.Layer[Witness]{Name: "city"}, // nil Scopes: everyone
	rules.Layer[Witness]{Name: "precinct", Scopes: func(w Witness) []string { return []string{w.Precinct} }},
)
reg.Register("city", "", rules.Rule[Witness]{
	ID: "log-statement", Event: "interview",
	Do: func(w Witness) error { fmt.Println("logged", w.Name); return nil },
})
reg.Register("precinct", "14th", rules.Rule[Witness]{
	ID: "lean-on-witness", Event: "interview", Priority: 5, Stop: rules.StopLayer,
	When: func(w Witness) bool { return w.Name != "" },
	Do:   func(w Witness) error { fmt.Println("leaned on", w.Name); return nil },
})

res, err := reg.Process(Witness{Name: "mabel", Precinct: "14th"}, "interview")
// res.Fired lists city/log-statement then precinct/lean-on-witness; err is nil
```

Conditions and actions compose with `logic.All`, `Any`, `Not`, `Sequence` and `If`. Runnable examples for a spaceport, an office and a noir sim are in [`rules/example_test.go`](rules/example_test.go).

### Spreading and inference

`spread` carries facts between holders with decay and your own filters; `infer` derives facts from facts. Both are deterministic and use the `knowledge` store. A spaceport sketch:

```go
// Anything with an ID and a store is a Holder.
type Crew struct { id string; kb *knowledge.Store; guild string }
func (c *Crew) ID() string                  { return c.id }
func (c *Crew) Knowledge() *knowledge.Store { return c.kb }

cfg := spread.DefaultConfig()
cfg.ReceivedKind = "hearsay"
// customs staff never pass smuggling intel to the haulers
noLeak := spread.Typed(func(e spread.TypedEncounter[*Crew]) bool {
	return !(e.Teller.guild == "customs" && e.Listener.guild == "haulers" && e.Fact.Type == "captain_is_smuggler")
})
sp, _ := spread.New(cfg, spread.WithFilter(noLeak))
res, _ := sp.Spread(ilse, []spread.Holder{broker, inspector}, now) // 0.9 arrives as 0.72, hop 1

engine, _ := infer.New(infer.DefaultConfig(), []infer.Rule{{
	ID: "flagged-and-seized-means-smuggler",
	Requires: []infer.Requirement{{Type: "ship_flagged"}, {Type: "cargo_seized"}},
	Produces: infer.Product{Type: "captain_is_smuggler", TTL: 5000},
	Confidence: 0.7,
}})
engine.Run(broker.kb, now) // derives the conclusion once; running again changes nothing
```

When the holders are one Go type, `spread.Typed` hands the filter typed values, so there is no hand assertion (a holder of another type is blocked, never a panic); `spread.TypedTransform` does the same for `Transform`. Compiled twin: `Example_typedFilter` in [`spread/typed_test.go`](spread/typed_test.go).

One optional `Transform` hook sees the teller, listener, tick and the teller's own copy, so distortion can depend on who is talking, to whom, and how old the news is. Fading is a step you call: `store.Fade(now)` lowers confidence by a policy (an exponential half-life per source kind from config, or your own `knowledge.Fader`), and forgets facts that fall below `forget_below`. It is off unless configured; the library has no clock.

```go
sp, _ := spread.New(spread.DefaultConfig())
sp.Transform = func(e spread.Encounter, d knowledge.Fact) knowledge.Fact {
	if e.Teller.ID() == "gossip-bram" { // who is talking
		d.Confidence += 0.1
	}
	if e.Listener.ID() == "sceptic-ola" { // to whom
		d.Confidence /= 2
	}
	if e.Now-e.Fact.Observed > 50 { // how old the news is
		d.Confidence /= 2
	}
	return d
}

cfg := knowledge.DefaultConfig()
cfg.Fade = knowledge.FadeConfig{HalfLives: map[string]int64{"overheard": 100}, ForgetBelow: 0.15}
store, _ := knowledge.NewStore(cfg)
store.Add(knowledge.Fact{ID: "salt", Type: "price_rumour", Subject: "salt", Confidence: 0.8, Source: "overheard"})
store.Add(knowledge.Fact{ID: "rope", Type: "price_rumour", Subject: "rope", Confidence: 0.8, Source: "witnessed"})
for _, now := range []knowledge.Tick{100, 200, 300} {
	res := store.Fade(now) // res.Changed and res.Forgotten list fact IDs
	salt, _ := store.Get("salt")
	fmt.Printf("%d changed: %v forgotten: %v salt: %.2f\n", now, res.Changed, res.Forgotten, salt.Confidence)
}
```

Compiled twins: the `Transform` body above is the same as in `Example_transformContext` in [`spread/example_test.go`](spread/example_test.go), and the `Fade` body is the same as in `Example_fadeConfig` in [`knowledge/example_test.go`](knowledge/example_test.go) (which wraps it in a helper; `ExampleStore_Fade` there shows a custom `Fader`). The `salt` rumour reads 0.40, 0.20, then is forgotten at tick 300 (0.10 < 0.15); the witnessed `rope` never fades.

Runnable examples in three domains: [`spread/example_test.go`](spread/example_test.go), [`infer/example_test.go`](infer/example_test.go).

### Declarative loading

The optional `declare` package loads config, rules, spreading filters and inference rules from JSON (or YAML through a decoder you supply) and binds them to conditions and actions you register by name. A hospital-ward sim lives entirely in [`declare/testdata/ward.json`](declare/testdata/ward.json):

```go
doc, err := declare.Load(file, nil) // nil decoder = JSON, unknown keys are errors
cat := declare.NewCatalog[Call]()
cat.RegisterKnowledgeOps(declare.Bindings[Call]{ /* Store, Now, Subject, EventID */ })
cat.RegisterCondition("role_is", func(a declare.Args) (logic.Condition[Call], error) { /* ... */ })
err = declare.LoadRules(registry, doc.Config.Rules, doc.Rules, cat) // all-or-nothing, errors carry paths
```

Relational rules need no Go condition: register your attribute lookup once with `RegisterAttributeOps` and use `attr_equals`, `attr_in` and `attr_same` from the data file. `add_fact` can set a fact's identity from data with `discriminator` (literal) or `discriminator_from` (a named source in `Bindings.Discriminators`). Compiled twins: `Example_officeDepts` and `Example_discriminator` in [`declare/example_test.go`](declare/example_test.go), data in [`declare/testdata/office_depts.json`](declare/testdata/office_depts.json).

```go
cat.RegisterAttributeOps(declare.Attributes[Call]{
	Sides:  []string{"entity"},
	Lookup: func(c Call, side, key string) (any, bool) { v, ok := c.self.attrs[key]; return v, ok },
}, declare.AttributeNames{}) // empty names = attr_equals, attr_in, attr_same
```

```json
{ "use": "attr_equals", "args": { "side": "entity", "key": "dept", "value": "finance" } }
{ "use": "add_fact", "args": { "type": "spilled_coffee", "confidence": 0.9, "source": "seen", "discriminator_from": "room" } }
```

YAML without a dependency in this module: pass `func(r io.Reader, v any) error { d := yaml.NewDecoder(r); d.KnownFields(true); return d.Decode(v) }` as the decoder.

## Observability

Everything is optional and does nothing by default; the core module stays standard-library only. Each component takes `log/slog` loggers and synchronous, read-only hooks, and repeated options chain:

```go
logger := slog.Default()
store, _ := knowledge.NewStore(kcfg, knowledge.WithLogger(logger), knowledge.WithHooks(knowledge.Hooks{
	OnAdd: func(f knowledge.Fact, r knowledge.AddResult) { /* add_fact, spread and infer all land here */ },
}))
sp, _ := spread.New(cfg, spread.WithLogger(logger), spread.WithHooks(spread.Hooks{
	OnSpread: func(r spread.Result, err error) { /* r.Dropped, r.HolderMismatches, joined store errors */ },
}))
engine, _ := infer.New(icfg, irules, infer.WithLogger(logger))
registry.SetLogger(logger)
registry.Observe(rules.Hooks{OnProcess: func(r rules.Result, err error) {}})
```

Results also say why nothing happened: `rules.Result.Layers` and `Unmatched()` (candidates and skipped rules per layer), `spread.Result.SkippedListeners` and `Withheld`, `infer.Result.Truncated`, `knowledge.FadeResult.Wired`, and `Store.Len()` with the size carried on `AddResult`, `FadeResult` and `PruneResult`. Timing is opt-in: give a component a clock (`spread.WithClock(time.Now)`, `infer.WithClock`, `knowledge.WithClock`, `registry.SetClock`) and the `*Done` / `OnDuration` hooks fire; without one nothing is timed.

For Prometheus or OpenTelemetry, the separate modules in [`contrib/`](contrib/README.md) wire all of this in a few lines.

## Development

```
just test        # go test ./... (root and both contrib modules)
just test-race   # go test -race ./...
just lint        # go vet ./... (root and both contrib modules)
just release-adapters vX.Y.Z   # pin adapters, print tag commands (never tags)
```

Module: `github.com/Whealinghold/rumour_mill`. Licence: MIT.
