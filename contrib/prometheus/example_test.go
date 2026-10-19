package rumourprom_test

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/rules"
	"github.com/Whealinghold/rumour_mill/spread"

	rumourprom "github.com/Whealinghold/rumour_mill/contrib/prometheus"
)

type npc struct {
	id string
	kb *knowledge.Store
}

func (n *npc) ID() string                  { return n.id }
func (n *npc) Knowledge() *knowledge.Store { return n.kb }

func Example() {
	reg := prometheus.NewRegistry()
	m, err := rumourprom.New(reg, rumourprom.WithSourceKinds("observed", "heard"))
	if err != nil {
		panic(err)
	}

	// 1. Store: counts adds from every caller, evictions and fades.
	mk := func(id string) *npc {
		kb, err := knowledge.NewStore(knowledge.DefaultConfig(), m.Store())
		if err != nil {
			panic(err)
		}
		return &npc{id: id, kb: kb}
	}
	ana, bo := mk("ana"), mk("bo")
	ana.kb.Add(knowledge.Fact{ID: "x", Type: "notice", Subject: "s", Confidence: 0.9, Source: "observed"})

	// 2. Spread: counts offers, deliveries, drops, errors and holder mismatches.
	sp, err := spread.New(spread.DefaultConfig(), m.Spread())
	if err != nil {
		panic(err)
	}
	if _, err := sp.Spread(ana, []spread.Holder{bo}, 1); err != nil {
		panic(err)
	}

	// 3. Infer: counts runs, passes and derived facts.
	eng, err := infer.New(infer.DefaultConfig(), []infer.Rule{{
		ID:         "heard",
		Requires:   []infer.Requirement{{Type: "notice"}},
		Produces:   infer.Product{Type: "rumour"},
		Confidence: 0.5,
	}}, m.Infer())
	if err != nil {
		panic(err)
	}
	if _, err := eng.Run(bo.kb, 2); err != nil {
		panic(err)
	}

	// 4. Rules: counts processed events and fired rules by layer.
	type ctx struct{}
	reg2, err := rules.NewRegistry(rules.DefaultConfig(), rules.Layer[ctx]{Name: "village"})
	if err != nil {
		panic(err)
	}
	m.ObserveRules(reg2) // seeds per-layer series and rules_registered
	if err := reg2.Register("village", "", rules.Rule[ctx]{ID: "r", Event: "market"}); err != nil {
		panic(err)
	}
	if _, err := reg2.Process(ctx{}, "market"); err != nil {
		panic(err)
	}

	fmt.Println("offered:", value(nil, reg, "rumour_spread_offered_total", "", ""))
	fmt.Println("delivered:", value(nil, reg, "rumour_spread_deliveries_total", "outcome", "inserted"))
	fmt.Println("adds inserted:", value(nil, reg, "rumour_fact_adds_total", "outcome", "inserted"))
	fmt.Println("derived:", value(nil, reg, "rumour_infer_derived_total", "outcome", "inserted"))
	fmt.Println("fired in village:", value(nil, reg, "rumour_rules_fired_total", "layer", "village"))
	fmt.Println("registered in village:", value(nil, reg, "rumour_rules_registered", "layer", "village"))
	fmt.Println("adds from observed:", value(nil, reg, "rumour_fact_adds_total", "source", "observed"))
	fmt.Println("largest store:", value(nil, reg, "rumour_store_size_max", "", ""))
	// Output:
	// offered: 1
	// delivered: 1
	// adds inserted: 3
	// derived: 1
	// fired in village: 1
	// registered in village: 1
	// adds from observed: 1
	// largest store: 2
}
