package declare

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/spread"
)

// Config gathers every section's config. Its JSON/YAML keys are knowledge,
// rules, spread and infer.
type Config struct {
	Knowledge knowledge.Config `json:"knowledge" yaml:"knowledge"`
	Rules     RulesConfig      `json:"rules" yaml:"rules"`
	Spread    spread.Config    `json:"spread" yaml:"spread"`
	Infer     infer.Config     `json:"infer" yaml:"infer"`
}

// DefaultConfig assembles each package's DefaultConfig; no default is
// duplicated here. Each call returns a fresh copy.
func DefaultConfig() Config {
	return Config{
		Knowledge: knowledge.DefaultConfig(),
		Rules:     DefaultRulesConfig(),
		Spread:    spread.DefaultConfig(),
		Infer:     infer.DefaultConfig(),
	}
}

// Validate runs every section's Validate and checks that the source kinds
// spreading and inference stamp (spread.received_kind, infer.derived_kind) are
// ranked in knowledge.source_ranks, since an unranked kind is almost always a
// typo. All problems are reported together, wrapping ErrInvalidConfig.
func (c Config) Validate() error {
	var errs []error
	for _, err := range []error{c.Knowledge.Validate(), c.Rules.Validate(), c.Spread.Validate(), c.Infer.Validate()} {
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: %w", ErrInvalidConfig, err))
		}
	}
	if _, ok := c.Knowledge.SourceRanks[c.Spread.ReceivedKind]; !ok {
		errs = append(errs, fmt.Errorf("%w: spread.received_kind %q is not in knowledge.source_ranks", ErrInvalidConfig, c.Spread.ReceivedKind))
	}
	if _, ok := c.Knowledge.SourceRanks[c.Infer.DerivedKind]; !ok {
		errs = append(errs, fmt.Errorf("%w: infer.derived_kind %q is not in knowledge.source_ranks", ErrInvalidConfig, c.Infer.DerivedKind))
	}
	return errors.Join(errs...)
}

// SpreadingSpec holds the spreading policy of a document.
type SpreadingSpec struct {
	// Filters are condition nodes over spread.Encounter; every one must pass.
	// Build them with Catalog[spread.Encounter].ConditionAll.
	Filters []Node `json:"filters,omitempty" yaml:"filters,omitempty"`
}

// Document is a whole declarative file. Every section except version is optional.
type Document struct {
	Version   int           `json:"version" yaml:"version"` // must be 1
	Config    Config        `json:"config" yaml:"config"`   // overlaid on DefaultConfig
	Rules     []RuleSpec    `json:"rules,omitempty" yaml:"rules,omitempty"`
	Spreading SpreadingSpec `json:"spreading,omitempty" yaml:"spreading,omitempty"`
	Inference []infer.Rule  `json:"inference,omitempty" yaml:"inference,omitempty"`
}

// Decoder fills v from r. JSONDecoder is the standard-library implementation;
// a YAML decoder is a few lines in the caller's own module, for example
//
//	func(r io.Reader, v any) error {
//		d := yaml.NewDecoder(r) // gopkg.in/yaml.v3
//		d.KnownFields(true)
//		return d.Decode(v)
//	}
type Decoder func(r io.Reader, v any) error

// JSONDecoder decodes one JSON value from r, rejecting unknown fields and
// trailing data.
func JSONDecoder(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("declare: unexpected data after the JSON document")
	}
	return nil
}

// Load decodes a Document with decode (nil means JSONDecoder). Config fields
// absent from the file keep their defaults, an explicit zero replaces them, and
// knowledge.source_ranks, if present, replaces the default map instead of
// merging with it. A version other than 1 is ErrUnsupportedVersion. Load does
// not validate the config or resolve any node; do that when wiring each section.
func Load(r io.Reader, decode Decoder) (Document, error) {
	if decode == nil {
		decode = JSONDecoder
	}
	defaults := DefaultConfig()
	doc := Document{Config: defaults}
	doc.Config.Knowledge.SourceRanks = nil // decoders would merge into a non-nil map; nil means "absent"
	if err := decode(r, &doc); err != nil {
		return Document{}, fmt.Errorf("declare: decoding document: %w", err)
	}
	if doc.Config.Knowledge.SourceRanks == nil {
		doc.Config.Knowledge.SourceRanks = defaults.Knowledge.SourceRanks
	}
	if doc.Version != 1 {
		return Document{}, fmt.Errorf("%w: got %d, want 1", ErrUnsupportedVersion, doc.Version)
	}
	return doc, nil
}
