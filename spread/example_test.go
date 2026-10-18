package spread_test

import (
	"fmt"

	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/spread"
)

// A spaceport hub: custom source kinds, two filters, a private fact that never
// travels, and a listener who is not told what they told us.
func Example_spaceport() {
	ranks := knowledge.DefaultConfig()
	ranks.SourceRanks = map[string]int{"witnessed": 3, "customs_notice": 3, "hearsay": 2, "deduced": 1}

	ilse, vo, oyelaran := newPerson("dockhand-ilse", ranks), newPerson("inspector-vo", ranks), newPerson("broker-oyelaran", ranks)
	ilse.group, vo.group, oyelaran.group = "haulers", "customs", "haulers"
	oyelaran.trust = map[string]int{"captain-rook": -80}

	ilse.kb.Add(knowledge.Fact{ID: "aurelia-seized", Type: "cargo_seized", Subject: "freighter-aurelia", Confidence: 0.9, Source: "witnessed"})
	ilse.kb.Add(knowledge.Fact{ID: "rook-grudge", Type: "captain_is_smuggler", Subject: "captain-rook", Confidence: 0.9, Source: "witnessed"})
	ilse.kb.Add(knowledge.Fact{ID: "own-debts", Type: "owes_money", Subject: "dockhand-ilse", Confidence: 1, Source: "witnessed", Private: true})

	cfg := spread.DefaultConfig()
	cfg.ReceivedKind = "hearsay"
	sp, err := spread.New(cfg,
		// Nobody tells a listener who distrusts the subject: they would only use it against them.
		spread.WithFilter(func(e spread.Encounter) bool { return e.Listener.(*person).trust[e.Fact.Subject] > -50 }),
		// Customs staff never pass smuggling intel to the haulers.
		spread.WithFilter(func(e spread.Encounter) bool {
			t, l := e.Teller.(*person), e.Listener.(*person)
			return !(t.group == "customs" && l.group == "haulers" && e.Fact.Type == "captain_is_smuggler")
		}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	show := func(from string, res spread.Result) {
		for _, t := range res.Transfers {
			fmt.Printf("%s -> %s: %s at %.3f (%s)\n", from, t.To, t.FactID, t.Confidence, t.Outcome)
		}
	}
	res, _ := sp.Spread(ilse, holders(oyelaran, vo), 1000)
	show("ilse", res)

	// Oyelaran got the seizure from Ilse, so Ilse is not told it back; Vo hears it a second time.
	res, _ = sp.Spread(oyelaran, holders(ilse, vo), 1001)
	show("oyelaran", res)

	got, _ := vo.kb.Get("aurelia-seized")
	fmt.Println("vo holds:", got.Source, got.Prov.From, got.Prov.Hops)

	// Output:
	// ilse -> broker-oyelaran: aurelia-seized at 0.720 (inserted)
	// ilse -> inspector-vo: aurelia-seized at 0.720 (inserted)
	// ilse -> inspector-vo: rook-grudge at 0.720 (inserted)
	// oyelaran -> inspector-vo: aurelia-seized at 0.576 (rejected)
	// vo holds: hearsay dockhand-ilse 1
}

// An office: gossip barely fades but never gets past the fourth retelling, and
// each retelling embellishes it a little.
func Example_office() {
	cfg := spread.DefaultConfig()
	cfg.DecayFactor = 0.9
	cfg.MaxHops = 4
	cfg.ReceivedKind = "grapevine"
	sp, _ := spread.New(cfg)
	sp.Transform = func(f knowledge.Fact) knowledge.Fact {
		f.Confidence += 0.05 // each retelling sounds a little surer
		f.Payload = fmt.Sprint(f.Payload, "!")
		return f
	}

	kcfg := knowledge.DefaultConfig()
	kcfg.SourceRanks = map[string]int{"announcement": 3, "overheard": 2, "grapevine": 2, "guess": 1}
	staff := []*person{newPerson("ann", kcfg), newPerson("bo", kcfg), newPerson("cy", kcfg), newPerson("di", kcfg), newPerson("ed", kcfg), newPerson("flo", kcfg)}
	staff[0].kb.Add(knowledge.Fact{ID: "layoffs", Type: "layoffs_planned", Subject: "company", Confidence: 0.5,
		Source: "overheard", Payload: "layoffs"})

	for i := 0; i+1 < len(staff); i++ {
		res, _ := sp.Spread(staff[i], holders(staff[i+1]), knowledge.Tick(i))
		if len(res.Transfers) == 0 {
			fmt.Printf("%s has nothing to pass to %s\n", staff[i].id, staff[i+1].id)
			continue
		}
		f, _ := staff[i+1].kb.Get("layoffs")
		fmt.Printf("%s heard %v at %.2f via %s, hop %d\n", staff[i+1].id, f.Payload, f.Confidence, f.Prov.From, f.Prov.Hops)
	}

	// Output:
	// bo heard layoffs! at 0.50 via ann, hop 1
	// cy heard layoffs!! at 0.50 via bo, hop 2
	// di heard layoffs!!! at 0.50 via cy, hop 3
	// ed heard layoffs!!!! at 0.50 via di, hop 4
	// ed has nothing to pass to flo
}

// A noir sim: a tip passes down a chain of informants, halving each time. It
// is delivered one hop after it becomes too weak to be retold, and a private
// fact never moves at all.
func Example_noir() {
	cfg := spread.DefaultConfig()
	cfg.DecayFactor = 0.5
	cfg.ReceivedKind = "rumour"
	sp, _ := spread.New(cfg)

	kcfg := knowledge.DefaultConfig()
	kcfg.SourceRanks = map[string]int{"testimony": 3, "tip": 2, "rumour": 1, "hunch": 0}
	chain := []*person{newPerson("detective", kcfg), newPerson("informant", kcfg), newPerson("barkeep", kcfg), newPerson("cabbie", kcfg)}
	chain[0].kb.Add(knowledge.Fact{ID: "alibi", Type: "alibi_false", Subject: "mr-vane", Confidence: 1, Source: "testimony"})
	chain[0].kb.Add(knowledge.Fact{ID: "source", Type: "informant_identity", Subject: "informant", Confidence: 1, Source: "testimony", Private: true})

	for i := 0; i+1 < len(chain); i++ {
		res, _ := sp.Spread(chain[i], holders(chain[i+1]), 0)
		if len(res.Transfers) == 0 {
			fmt.Printf("%s tells %s nothing\n", chain[i].id, chain[i+1].id)
		}
		for _, t := range res.Transfers {
			fmt.Printf("%s tells %s: %s at %.2f\n", chain[i].id, t.To, t.FactID, t.Confidence)
		}
	}

	// Output:
	// detective tells informant: alibi at 0.50
	// informant tells barkeep: alibi at 0.25
	// barkeep tells cabbie nothing
}
