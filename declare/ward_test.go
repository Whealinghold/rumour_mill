package declare_test

import (
	"fmt"

	"github.com/Whealinghold/rumour_mill/declare"
	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/rules"
	"github.com/Whealinghold/rumour_mill/spread"
)

// The generality proof: a hospital-ward staff sim written entirely as
// testdata/ward.json plus the Go below, which only defines the caller's own
// types and registers the names the data refers to. No library code changed to
// support it. The setup region is what a ward needs; the Example after it only
// drives the simulation and prints.

// --- ward setup begin ---
type staff struct {
	id, role string
	kb       *knowledge.Store
}

func (s *staff) ID() string                  { return s.id }
func (s *staff) Knowledge() *knowledge.Store { return s.kb }

type call struct { // what a rule sees: who, about which patient, when
	who     *staff
	patient string
	at      knowledge.Tick
}

func newWard() (*rules.Registry[call], *spread.Spreader, *infer.Engine, declare.Config, error) {
	doc, err := declare.Load(must1(testdata.Open("testdata/ward.json")), nil)
	if err != nil {
		return nil, nil, nil, doc.Config, err
	}
	rc := declare.NewCatalog[call]()
	rc.RegisterKnowledgeOps(declare.Bindings[call]{
		Store:   func(c call) *knowledge.Store { return c.who.kb },
		Now:     func(c call) knowledge.Tick { return c.at },
		Subject: func(c call) string { return c.patient },
		EventID: func(c call) string { return fmt.Sprint(c.patient, "@", c.at) },
	})
	rc.RegisterAttributeOps(declare.Attributes[call]{Sides: []string{"entity"},
		Lookup: func(c call, _, k string) (any, bool) { return c.who.role, k == "role" }}, declare.AttributeNames{})
	fc := declare.NewCatalog[spread.Encounter]()
	fc.RegisterAttributeOps(declare.Attributes[spread.Encounter]{Sides: []string{"teller", "listener"},
		Lookup: func(e spread.Encounter, side, k string) (any, bool) {
			h := e.Teller
			if side == "listener" {
				h = e.Listener
			}
			return h.(*staff).role, k == "role"
		}}, declare.AttributeNames{})
	reg, err := rules.NewRegistry(doc.Config.Rules.Config,
		rules.Layer[call]{Name: "ward"},
		rules.Layer[call]{Name: "role", Scopes: func(c call) []string { return []string{c.who.role} }})
	if err != nil {
		return nil, nil, nil, doc.Config, err
	}
	filters, err := fc.ConditionAll(doc.Spreading.Filters)
	if err != nil {
		return nil, nil, nil, doc.Config, err
	}
	sp, _ := spread.New(doc.Config.Spread, spread.WithFilter(filters))
	eng, _ := infer.New(doc.Config.Infer, doc.Inference)
	return reg, sp, eng, doc.Config, declare.LoadRules(reg, doc.Config.Rules, doc.Rules, rc)
}

// --- ward setup end ---

func must1[T any](v T, err error) T {
	must(err)
	return v
}

func Example_ward() {
	reg, sp, eng, cfg, err := newWard()
	if err != nil || cfg.Validate() != nil {
		fmt.Println("setup failed:", err, cfg.Validate())
		return
	}
	hire := func(id, role string) *staff {
		return &staff{id: id, role: role, kb: must1(knowledge.NewStore(cfg.Knowledge))}
	}
	alice, bo, carol, ito := hire("alice", "nurse"), hire("bo", "porter"), hire("carol", "visitor"), hire("dr-ito", "doctor")
	all := []*staff{alice, bo, carol, ito}

	for _, s := range all { // a monitor alarm sounds at bed 7
		res, _ := reg.Process(call{s, "bed-7", 100}, "monitor_alarm")
		fmt.Println(s.id, "fired", len(res.Fired))
	}
	alice.kb.Add(knowledge.Fact{Type: "labs_abnormal", Subject: "bed-7", Confidence: 0.8, Source: "charted"})
	res, _ := eng.Run(alice.kb, 101)
	fmt.Println("alice derived", len(res.Derived), "facts in", res.Passes, "passes")

	out, _ := sp.Spread(alice, []spread.Holder{bo, carol, ito}, 102)
	fmt.Println("alice's transfers:", len(out.Transfers), "(the visitor is filtered out)")
	vitals := bo.kb.Find(knowledge.Query{Type: "vitals_concerning", Sources: []string{"corridor_talk"}}, 102)[0]
	fmt.Printf("bo now holds vitals at %.3f from %s via %s\n", vitals.Confidence, vitals.Prov.From, vitals.Source)
	fmt.Println("carol knows", len(carol.kb.All()), "facts")

	for _, s := range all { // shift handover: clinicians who suspect a problem flag it
		reg.Process(call{s, "bed-7", 120}, "shift_handover")
	}
	pending := ito.kb.Find(knowledge.Query{Type: "escalation_pending"}, 120)
	fmt.Println("dr-ito has escalation pending:", len(pending) == 1, "| bo:", len(bo.kb.Find(knowledge.Query{Type: "escalation_pending"}, 120)))

	// Output:
	// alice fired 1
	// bo fired 1
	// carol fired 0
	// dr-ito fired 0
	// alice derived 2 facts in 2 passes
	// alice's transfers: 8 (the visitor is filtered out)
	// bo now holds vitals at 0.765 from alice via corridor_talk
	// carol knows 0 facts
	// dr-ito has escalation pending: true | bo: 0
}
