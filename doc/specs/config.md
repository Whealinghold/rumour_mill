# Spec: config

Package: `declare` (aggregate config and document); each section's struct lives in its own package. Status: approved with changes; implemented (Phase 5).

## 1. Purpose

One place listing every tunable with its documented default, the aggregate config struct, and the YAML/JSON document format for config, rules, spreading filters and inference rules. Per-package behaviour is in the other specs.

## 2. Public API

```go
package declare

type Config struct {
    Knowledge knowledge.Config `json:"knowledge" yaml:"knowledge"`
    Rules     RulesConfig      `json:"rules" yaml:"rules"` // rules.Config + default_priority
    Spread    spread.Config    `json:"spread" yaml:"spread"`
    Infer     infer.Config     `json:"infer" yaml:"infer"`
}

// DefaultConfig calls each package's DefaultConfig; no default is duplicated here.
func DefaultConfig() Config

// Validate runs each section's Validate plus the cross-section rules in section 4.
func (c Config) Validate() error

// Document is the whole declarative file. Every section is optional.
type Document struct {
    Version   int                 `json:"version" yaml:"version"` // must be 1
    Config    Config              `json:"config" yaml:"config"`   // overlaid on defaults
    Rules     []RuleSpec          `json:"rules,omitempty" yaml:"rules,omitempty"`
    Spreading SpreadingSpec       `json:"spreading,omitempty" yaml:"spreading,omitempty"`
    Inference []infer.Rule        `json:"inference,omitempty" yaml:"inference,omitempty"`
}

type SpreadingSpec struct {
    Filters []Node `json:"filters,omitempty" yaml:"filters,omitempty"` // each must pass
}

// Decoder fills v from r. JSONDecoder is provided; a YAML decoder is a caller one-liner.
type Decoder func(r io.Reader, v any) error
func JSONDecoder(r io.Reader, v any) error // encoding/json with DisallowUnknownFields

// Load decodes a Document. Config fields absent from the file keep their defaults.
func Load(r io.Reader, decode Decoder) (Document, error)
```

YAML without a dependency in this module:

```go
doc, err := declare.Load(f, func(r io.Reader, v any) error {
    dec := yaml.NewDecoder(r) // gopkg.in/yaml.v3, in the caller's go.mod
    dec.KnownFields(true)
    return dec.Decode(v)
})
```

Every config and document struct carries `json` and `yaml` tags with identical snake_case names, so the same content works in either syntax. After `Load` the caller wires things explicitly:

```go
cfg := doc.Config // DefaultConfig overlaid; call cfg.Validate()
store, _ := knowledge.NewStore(cfg.Knowledge)
reg, _ := rules.NewRegistry[Ctx](cfg.Rules, layers...)
_ = declare.LoadRules(reg, cfg.Rules, doc.Rules, ruleCatalog)
filters, _ := spreadCatalog.ConditionAll(doc.Spreading.Filters)
sp, _ := spread.New(cfg.Spread, spread.WithFilter(filters))
eng, _ := infer.New(cfg.Infer, doc.Inference)
```

## 3. Loading semantics

- **Overlay.** `Load` decodes into a `Document` whose `Config` was pre-filled with `DefaultConfig()`: a field present in the file replaces the default, an absent one keeps it, and an explicit zero (`max_hops: 0`) is distinguishable from omission.
- **Maps replace.** `knowledge.source_ranks`, if present, replaces the default map entirely.
- **Embedded section.** `declare.RulesConfig` embeds `rules.Config` (`json:",inline" yaml:",inline"`), so `on_action_error` and `default_priority` sit side by side under `rules:`. Unknown keys are rejected there too.
- **Decoders.** A nil `Decoder` means `JSONDecoder`, which also rejects trailing data after the document. A custom decoder starts from the defaults like any other.- **Unknown keys are errors** (`DisallowUnknownFields` in `JSONDecoder`, `KnownFields(true)` in the YAML example). A misspelt tunable fails loudly.
- **Version.** `version` is required and must be `1`, else `ErrUnsupportedVersion`.
- **Numbers.** Ticks and counts are integers; confidences and factors are floats.
- **Data only.** No expressions, includes or environment interpolation. Behaviour comes from names the caller registered.
- **Validation order.** `Load` decodes and checks the version. `Config.Validate`, `LoadRules`, `Catalog.Condition*` and `infer.New` each validate their own section when wired and report all problems (joined), with paths such as `rules[3].when.of[1]`.

## 4. Validation

`Config.Validate` runs each section's `Validate` and these cross-section rules (errors joined, wrapping `ErrInvalidConfig`):

1. `spread.received_kind` must be a key of `knowledge.source_ranks`.
2. `infer.derived_kind` must be a key of `knowledge.source_ranks`.

An unranked kind silently gets `unranked_rank`, almost always a typo in a hand-edited file. No ordering is enforced between `spread.min_confidence` and `spread.min_source_confidence`.

## 5. Complete tunables

Every numeric or behavioural default. Per `doc/rules/testing.md` rule 4, each row gets a test asserting the default and a test that overriding it changes behaviour. Every row is implemented and tested (`rules.default_priority` in `declare/rules_test.go`). Zero values: `knowledge.Config{}` and `rules.Config{}` are valid; `spread.Config{}` and `infer.Config{}` are rejected by `Validate` (no sensible zero meaning), so start from `DefaultConfig()`.

| Key (under `config:`) | Type | Default | Spec | Exercised by |
| --- | --- | --- | --- | --- |
| `knowledge.source_ranks` | map string→int | `{observed: 3, relayed: 2, derived: 1}` | [knowledge](knowledge.md) | both knowledge examples |
| `knowledge.unranked_rank` | int | `0` | knowledge | config test |
| `knowledge.query_floor` | float [0,1] | `0.2` | knowledge | spaceport example, config test |
| `knowledge.max_facts` | int ≥0 (0 = unlimited) | `0` | knowledge | office example |
| `rules.default_priority` | int | `100` | [declarative](declarative.md) (loader only) | declarative tests |
| `rules.on_action_error` | `continue` \| `stop` (empty = `continue`) | `continue` | [rules](rules.md) | noir example, rules tests |
| `spread.decay_factor` | float (0,1] | `0.8` | [spreading](spreading-and-inference.md) | both spread examples |
| `spread.min_confidence` | float [0,1] | `0.2` | spreading | spaceport example |
| `spread.min_source_confidence` | float [0,1] | `0.3` | spreading | spaceport example |
| `spread.max_hops` | int ≥0 (0 = unlimited) | `0` | spreading | office example |
| `spread.received_kind` | string | `relayed` | spreading | both |
| `spread.skip_back_to_source` | bool | `true` | spreading | spaceport example |
| `infer.max_passes` | int ≥1 | `1` | spreading (part B) | office inference example |
| `infer.id_prefix` | string | `inferred` | spreading (part B) | spaceport inference example |
| `infer.derived_kind` | string | `derived` | spreading (part B) | both |

Not tunable by design (they define the scale or semantics): confidence range `[0,1]`; expiry comparison `Expiry <= now`; reserved combinator names `all any not seq if`; the U+001F key separator.

Values that belong to the data they describe, not global config: `Rule.Priority`, `Rule.Stop`, `Query.MinConfidence`, `Requirement.MinConfidence`, `Rule.Confidence`, `Product.TTL`, and node `args`.

## 6. Document grammar

```
document   := { version: 1, config?: Config, rules?: [RuleSpec], spreading?: { filters: [Node] }, inference?: [InferRule] }
RuleSpec   := { id, layer, scope?, event, priority?, stop?: layer|all, when?: Node, do?: [Node] }
Node       := { use: <name>, args?: {..} }
            | { use: all|any, of: [Node...] } | { use: not, of: [Node] }
            | { use: seq, of: [Node...] }     | { use: if, cond: Node, then: Node, else?: Node }
InferRule  := { id, requires: [Requirement], produces: Product, confidence }
Requirement:= { type, subject?, min_confidence?, sources?: [string] }
Product    := { type, subject?, ttl?, private?, payload? }
```

Semantics: [declarative](declarative.md), [rules](rules.md), [spreading and inference](spreading-and-inference.md).

## 7. Errors

`ErrUnsupportedVersion`; `ErrInvalidConfig` (any `Validate` failure, including section 4); decoder errors wrapped and returned from `Load` (unknown key, wrong type, syntax). Section-level errors are defined in their own specs.

## 8. Concurrency and determinism

`Config` and `Document` are plain values. `DefaultConfig()` returns a fresh copy each time (including a fresh `source_ranks` map). `Load` has no shared state and is deterministic.

## 9. Original hardcoded thing → config key

| Original constant | Config key | Default |
| --- | --- | --- |
| `DegradationFactor = 0.8` | `spread.decay_factor` | `0.8` |
| `MinSpreadConfidence = 0.2` | `spread.min_confidence` | `0.2` |
| `Spreadable` floor `0.3` | `spread.min_source_confidence` | `0.3` |
| Query floor `0.2` | `knowledge.query_floor` | `0.2` |
| `sourceStrength` `3/3/2/1/0` | `knowledge.source_ranks`, `unranked_rank` | `observed 3, relayed 2, derived 1`; `0` |
| Source stamped on spread copies | `spread.received_kind` | `relayed` |
| Source stamped on inferred facts | `infer.derived_kind` | `derived` |
| Single inference pass | `infer.max_passes` | `1` |
| Derived-ID prefix | `infer.id_prefix` | `inferred` |
| Attitude thresholds `-25`, `25`, `-50` | node `args` in caller conditions | none shipped |
| Fixed layer set | caller `Layer[C]` list | none shipped |
| Priority with no default | `rules.default_priority` | `100` |
| Actions could not fail | `rules.on_action_error` | `continue` |

## 10. Worked examples

### Spaceport trading hub: `port.yaml`

```yaml
version: 1
config:
  knowledge:
    source_ranks: { witnessed: 3, customs_notice: 3, hearsay: 2, deduced: 1 }
    query_floor: 0.25
  spread:
    received_kind: hearsay
  infer:
    derived_kind: deduced
rules:
  - id: witness-inspection
    layer: station
    event: cargo_inspected
    priority: 10
    when: { use: on_same_dock }
    do: [ { use: add_fact, args: { type: cargo_seized, confidence: 0.9, source: witnessed, ttl: 2000, per_event: true } } ]
spreading:
  filters:
    - use: not
      of: [ { use: listener_distrusts_subject, args: { at_most: -50 } } ]
inference:
  - id: flagged-and-seized-means-smuggler
    requires: [ { type: ship_flagged, min_confidence: 0.4 }, { type: cargo_seized, min_confidence: 0.5 } ]
    produces: { type: captain_is_smuggler, ttl: 5000 }
    confidence: 0.7
```

### Office-gossip sim: `office.json`

```json
{
  "version": 1,
  "config": {
    "knowledge": { "source_ranks": { "announcement": 3, "overheard": 2, "grapevine": 2, "guess": 1 }, "max_facts": 40 },
    "spread": { "decay_factor": 0.9, "max_hops": 4, "received_kind": "grapevine" },
    "infer": { "derived_kind": "guess", "max_passes": 2 }
  },
  "rules": [
    { "id": "overhear-closed-door-meeting", "layer": "company", "event": "meeting_held",
      "when": { "use": "within_earshot" },
      "do": [ { "use": "add_fact", "args": { "type": "layoffs_planned", "confidence": 0.45, "source": "overheard", "ttl": 240 } } ] }
  ],
  "spreading": {
    "filters": [
      { "use": "not", "of": [ { "use": "all", "of": [
        { "use": "listener_role_is", "args": { "role": "manager" } },
        { "use": "fact_type_in", "args": { "types": ["layoffs_planned", "promotion_pending"] } } ] } ] }
    ]
  },
  "inference": [
    { "id": "freeze-plus-late-meetings",
      "requires": [ { "type": "hiring_freeze", "subject": "company" }, { "type": "late_meetings_seen", "subject": "company", "min_confidence": 0.3 } ],
      "produces": { "type": "layoffs_planned", "subject": "company" }, "confidence": 0.4 }
  ]
}
```
