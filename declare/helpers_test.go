package declare_test

import (
	"embed"

	"github.com/Whealinghold/rumour_mill/declare"
	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/logic"
	"github.com/Whealinghold/rumour_mill/rules"
	"github.com/Whealinghold/rumour_mill/spread"
)

//go:embed testdata/*.json
var testdata embed.FS

// being is a generic caller type used by the three shared examples: an ID, some
// string attributes and a knowledge store. It satisfies spread.Holder.
type being struct {
	id    string
	attrs map[string]string
	kb    *knowledge.Store
}

func (b *being) ID() string                  { return b.id }
func (b *being) Knowledge() *knowledge.Store { return b.kb }

// scene is the context rules see: who is being processed and what happened.
type scene struct {
	self           *being
	subject, evtID string
	place          string
	now            knowledge.Tick
}

// sceneCatalog registers the knowledge operations plus two small conditions.
func sceneCatalog() *declare.Catalog[scene] {
	cat := declare.NewCatalog[scene]()
	must(cat.RegisterKnowledgeOps(declare.Bindings[scene]{
		Store:   func(s scene) *knowledge.Store { return s.self.kb },
		Now:     func(s scene) knowledge.Tick { return s.now },
		Subject: func(s scene) string { return s.subject },
		EventID: func(s scene) string { return s.evtID },
	}))
	must(cat.RegisterCondition("here", func(a declare.Args) (logic.Condition[scene], error) {
		return func(s scene) bool { return s.self.attrs["place"] == s.place }, a.Unknown()
	}))
	must(cat.RegisterCondition("attr_is", func(a declare.Args) (logic.Condition[scene], error) {
		name, err := a.String("name")
		if err != nil {
			return nil, err
		}
		value, err := a.String("value")
		if err != nil {
			return nil, err
		}
		return func(s scene) bool { return s.self.attrs[name] == value }, a.Unknown("name", "value")
	}))
	must(cat.RegisterAttributeOps(declare.Attributes[scene]{Sides: []string{"entity"}, Lookup: func(s scene, _, k string) (any, bool) {
		v, ok := s.self.attrs[k]
		return v, ok
	}}, declare.AttributeNames{}))
	return cat
}

// encounterCatalog registers the conditions spreading filters use.
func encounterCatalog() *declare.Catalog[spread.Encounter] {
	cat := declare.NewCatalog[spread.Encounter]()
	attr := func(listener bool) func(declare.Args) (logic.Condition[spread.Encounter], error) {
		return func(a declare.Args) (logic.Condition[spread.Encounter], error) {
			name, err := a.String("name")
			if err != nil {
				return nil, err
			}
			value, err := a.String("value")
			if err != nil {
				return nil, err
			}
			return func(e spread.Encounter) bool {
				h := e.Teller
				if listener {
					h = e.Listener
				}
				return h.(*being).attrs[name] == value
			}, a.Unknown("name", "value")
		}
	}
	must(cat.RegisterAttributeOps(declare.Attributes[spread.Encounter]{Sides: []string{"teller", "listener"}, Lookup: func(e spread.Encounter, side, k string) (any, bool) {
		h := e.Teller
		if side == "listener" {
			h = e.Listener
		}
		v, ok := h.(*being).attrs[k]
		return v, ok
	}}, declare.AttributeNames{}))
	must(cat.RegisterCondition("teller_attr_is", attr(false)))
	must(cat.RegisterCondition("listener_attr_is", attr(true)))
	must(cat.RegisterCondition("fact_type_in", func(a declare.Args) (logic.Condition[spread.Encounter], error) {
		types, err := a.Strings("types")
		if err != nil {
			return nil, err
		}
		return func(e spread.Encounter) bool {
			for _, t := range types {
				if e.Fact.Type == t {
					return true
				}
			}
			return false
		}, a.Unknown("types")
	}))
	return cat
}

// world is everything a document builds.
type world struct {
	cfg declare.Config
	doc declare.Document
	reg *rules.Registry[scene]
	sp  *spread.Spreader
	eng *infer.Engine
}

// load builds a world from an embedded document. It panics on error: the
// documents are part of the test data and must be valid.
func load(file string, layers ...rules.Layer[scene]) *world {
	f, err := testdata.Open("testdata/" + file)
	must(err)
	defer f.Close()
	doc, err := declare.Load(f, nil)
	must(err)
	must(doc.Config.Validate())

	reg, err := rules.NewRegistry(doc.Config.Rules.Config, layers...)
	must(err)
	must(declare.LoadRules(reg, doc.Config.Rules, doc.Rules, sceneCatalog()))

	filters, err := encounterCatalog().ConditionAll(doc.Spreading.Filters)
	must(err)
	sp, err := spread.New(doc.Config.Spread, spread.WithFilter(filters))
	must(err)
	eng, err := infer.New(doc.Config.Infer, doc.Inference)
	must(err)
	return &world{cfg: doc.Config, doc: doc, reg: reg, sp: sp, eng: eng}
}

func (w *world) being(id string, kv ...string) *being {
	kb, err := knowledge.NewStore(w.cfg.Knowledge)
	must(err)
	b := &being{id: id, attrs: map[string]string{}, kb: kb}
	for i := 0; i+1 < len(kv); i += 2 {
		b.attrs[kv[i]] = kv[i+1]
	}
	return b
}

// attrLayer is a layer whose scope key is one of the being's attributes.
func attrLayer(name, attr string) rules.Layer[scene] {
	return rules.Layer[scene]{Name: name, Scopes: func(s scene) []string { return []string{s.self.attrs[attr]} }}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
