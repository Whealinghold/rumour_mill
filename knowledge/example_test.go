package knowledge_test

import (
	"fmt"

	"github.com/Whealinghold/rumour_mill/knowledge"
)

// A spaceport hub: a dockhand hears about a seizure, later sees it first-hand,
// and a second-hand retelling can no longer overwrite what was witnessed.
func Example_spaceport() {
	cfg := knowledge.DefaultConfig()
	cfg.SourceRanks = map[string]int{"witnessed": 3, "customs_notice": 3, "hearsay": 2, "deduced": 1}
	cfg.QueryFloor = 0.25
	store, err := knowledge.NewStore(cfg)
	if err != nil {
		fmt.Println(err)
		return
	}

	seized := func(source string, confidence float64, at knowledge.Tick) knowledge.Fact {
		return knowledge.Fact{
			Type: "cargo_seized", Subject: "freighter-aurelia", Discriminator: "bay-4",
			Confidence: confidence, Source: source, Observed: at,
		}
	}
	for _, f := range []knowledge.Fact{
		seized("hearsay", 0.6, 1040),
		seized("witnessed", 0.9, 1043),
		seized("hearsay", 0.95, 1050), // lower rank loses whatever its confidence
	} {
		res, _ := store.Add(f)
		fmt.Println(f.Source, f.Confidence, res.Outcome)
	}

	// Below the 0.25 query floor: stored, but invisible to default queries.
	store.Add(knowledge.Fact{Type: "fuel_price_spike", Subject: "hub-prime", Confidence: 0.2, Source: "deduced"})

	visible := store.Find(knowledge.Query{}, 1060)
	fmt.Println("stored:", len(store.All()), "visible:", len(visible))
	fmt.Println(visible[0].Type, visible[0].Source, visible[0].Confidence)

	// Output:
	// hearsay 0.6 inserted
	// witnessed 0.9 replaced
	// hearsay 0.95 rejected
	// stored: 2 visible: 1
	// cargo_seized witnessed 0.9
}

// An office-gossip sim: people forget (a cap of two facts), rumours expire,
// and "overheard" and "grapevine" share a rank so the stronger telling wins.
func Example_office() {
	cfg := knowledge.DefaultConfig()
	cfg.SourceRanks = map[string]int{"announcement": 3, "overheard": 2, "grapevine": 2, "guess": 1}
	cfg.MaxFacts = 2
	store, err := knowledge.NewStore(cfg)
	if err != nil {
		fmt.Println(err)
		return
	}

	expiry := knowledge.Tick(220)
	promo := func(confidence float64) knowledge.Fact {
		return knowledge.Fact{
			ID: "promo-dana", Type: "promotion_pending", Subject: "dana-from-accounts",
			Confidence: confidence, Source: "grapevine", Observed: 100, Expiry: &expiry,
		}
	}
	for _, f := range []knowledge.Fact{
		promo(0.5),
		promo(0.7), // equal rank, higher confidence: replaces
		promo(0.4), // equal rank, lower confidence: rejected
		{ID: "layoffs", Type: "layoffs_planned", Subject: "company", Confidence: 0.3, Source: "guess", Observed: 110},
		{ID: "reorg", Type: "reorg_coming", Subject: "company", Confidence: 0.9, Source: "announcement", Observed: 120},
	} {
		res, _ := store.Add(f)
		fmt.Printf("%s %.1f %s evicted=%v\n", f.ID, f.Confidence, res.Outcome, res.Evicted)
	}

	ids := func(now knowledge.Tick) (out []string) {
		for _, f := range store.Find(knowledge.Query{}, now) {
			out = append(out, f.ID)
		}
		return out
	}
	fmt.Println("at 120:", ids(120))
	fmt.Println("at 220:", ids(220)) // expiry <= now means expired
	fmt.Println("pruned:", store.Prune(220))

	// Output:
	// promo-dana 0.5 inserted evicted=[]
	// promo-dana 0.7 replaced evicted=[]
	// promo-dana 0.4 rejected evicted=[]
	// layoffs 0.3 inserted evicted=[]
	// reorg 0.9 inserted evicted=[layoffs]
	// at 120: [promo-dana reorg]
	// at 220: [reorg]
	// pruned: 1
}

// A harbour market where rumours fade by a custom linear curve: a witnessed
// fact never weakens, an overheard one loses 0.1 per 100 ticks, and anything
// below 0.15 is forgotten.
func ExampleStore_Fade() {
	cfg := knowledge.DefaultConfig()
	cfg.Fade.ForgetBelow = 0.15
	linear := func(f knowledge.Fact, now, elapsed knowledge.Tick) (float64, bool) {
		if f.Source == "witnessed" {
			return f.Confidence, false
		}
		return f.Confidence - 0.1*float64(elapsed)/100, false
	}
	store, _ := knowledge.NewStore(cfg, knowledge.WithFader(linear))
	store.Add(knowledge.Fact{ID: "salt", Type: "price_rumour", Subject: "salt", Confidence: 0.4, Source: "overheard"})
	store.Add(knowledge.Fact{ID: "rope", Type: "price_rumour", Subject: "rope", Confidence: 0.9, Source: "witnessed"})

	for _, now := range []knowledge.Tick{100, 200, 300} {
		res := store.Fade(now)
		fmt.Println(now, "changed:", res.Changed, "forgotten:", res.Forgotten)
	}

	// Output:
	// 100 changed: [salt] forgotten: []
	// 200 changed: [salt] forgotten: []
	// 300 changed: [] forgotten: [salt]
}

// The same harbour market, faded by the built-in half-life policy from config
// alone: overheard rumours halve every 100 ticks, witnessed facts never fade,
// and anything below 0.15 is forgotten. Mirrors the README Fade snippet.
func Example_fadeConfig() {
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

	// Output:
	// 100 changed: [salt] forgotten: [] salt: 0.40
	// 200 changed: [salt] forgotten: [] salt: 0.20
	// 300 changed: [] forgotten: [salt] salt: 0.00
}
