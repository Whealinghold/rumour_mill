package infer_test

import (
	"fmt"

	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
)

func newStore(ranks map[string]int) *knowledge.Store {
	cfg := knowledge.DefaultConfig()
	cfg.SourceRanks = ranks
	s, err := knowledge.NewStore(cfg)
	if err != nil {
		panic(err)
	}
	return s
}

// A spaceport hub: a broker who knows a ship was flagged and its cargo seized
// concludes the captain is a smuggler. The conclusion remembers its premises,
// expires, and is not derived twice.
func Example_spaceport() {
	store := newStore(map[string]int{"witnessed": 3, "hearsay": 2, "deduced": 1})
	store.Add(knowledge.Fact{ID: "flag-1", Type: "ship_flagged", Subject: "freighter-aurelia", Confidence: 0.6, Source: "hearsay"})
	store.Add(knowledge.Fact{ID: "seized-1", Type: "cargo_seized", Subject: "freighter-aurelia", Confidence: 0.9, Source: "witnessed"})
	store.Add(knowledge.Fact{ID: "seized-2", Type: "cargo_seized", Subject: "freighter-borealis", Confidence: 0.9, Source: "witnessed"})

	cfg := infer.DefaultConfig()
	cfg.DerivedKind = "deduced"
	engine, err := infer.New(cfg, []infer.Rule{{
		ID: "flagged-and-seized-means-smuggler",
		Requires: []infer.Requirement{
			{Type: "ship_flagged", MinConfidence: ptr(0.4)},
			{Type: "cargo_seized", MinConfidence: ptr(0.5), Sources: []string{"witnessed", "hearsay"}},
		},
		Produces:   infer.Product{Type: "captain_is_smuggler", TTL: 5000},
		Confidence: 0.7,
	}})
	if err != nil {
		fmt.Println(err)
		return
	}

	res, _ := engine.Run(store, 1000)
	for _, d := range res.Derived {
		f, _ := store.Get(d.ID)
		fmt.Println(d.ID, d.Outcome)
		fmt.Println(f.Type, f.Subject, f.Confidence, f.Source, f.Prov.Premises, *f.Expiry)
	}
	again, _ := engine.Run(store, 1001)
	fmt.Println("second run derived:", len(again.Derived))
	fmt.Println("visible after expiry:", len(store.Find(knowledge.Query{Type: "captain_is_smuggler"}, 6000)))

	// Output:
	// inferred:flagged-and-seized-means-smuggler:freighter-aurelia inserted
	// captain_is_smuggler freighter-aurelia 0.7 deduced [flag-1 seized-1] 6000
	// second run derived: 0
	// visible after expiry: 0
}

// An office: a hiring freeze plus late meetings suggests layoffs, and layoffs
// suggest a reorganisation. Chaining is opt-in through MaxPasses.
func Example_office() {
	build := func(maxPasses int) []string {
		store := newStore(map[string]int{"announcement": 3, "overheard": 2, "guess": 1})
		store.Add(knowledge.Fact{Type: "hiring_freeze", Subject: "company", Confidence: 0.9, Source: "announcement"})
		store.Add(knowledge.Fact{Type: "late_meetings_seen", Subject: "company", Confidence: 0.5, Source: "overheard"})

		cfg := infer.DefaultConfig()
		cfg.MaxPasses = maxPasses
		cfg.DerivedKind = "guess"
		engine, _ := infer.New(cfg, []infer.Rule{
			{ID: "freeze-plus-late-meetings",
				Requires: []infer.Requirement{
					{Type: "hiring_freeze", Subject: "company"},
					{Type: "late_meetings_seen", Subject: "company", MinConfidence: ptr(0.3)}},
				Produces: infer.Product{Type: "layoffs_planned", Subject: "company"}, Confidence: 0.4},
			{ID: "layoffs-then-reorg",
				Requires: []infer.Requirement{{Type: "layoffs_planned", Subject: "company", MinConfidence: ptr(0.35)}},
				Produces: infer.Product{Type: "reorg_coming", Subject: "company"}, Confidence: 0.5},
		})
		res, _ := engine.Run(store, 10)
		out := []string{fmt.Sprint("passes=", res.Passes)}
		for _, d := range res.Derived {
			f, _ := store.Get(d.ID)
			out = append(out, f.Type)
		}
		return out
	}
	fmt.Println("max_passes 1:", build(1))
	fmt.Println("max_passes 2:", build(2))
	fmt.Println("max_passes 9:", build(9)) // stops early: pass 3 adds nothing

	// Output:
	// max_passes 1: [passes=1 layoffs_planned]
	// max_passes 2: [passes=2 layoffs_planned reorg_coming]
	// max_passes 9: [passes=3 layoffs_planned reorg_coming]
}

// A noir sim: requirements without a subject share one bound subject, so the
// rule fires per suspect who satisfies all of them; a fixed-subject requirement
// is checked on its own. The conclusion is private to the detective.
func Example_noir() {
	store := newStore(map[string]int{"testimony": 3, "tip": 2, "hunch": 1, "derived": 0})
	add := func(typ, subject, source string, c float64) {
		store.Add(knowledge.Fact{Type: typ, Subject: subject, Confidence: c, Source: source, Discriminator: source})
	}
	add("alibi_broken", "mr-vane", "testimony", 0.9)
	add("motive_known", "mr-vane", "tip", 0.6)
	add("alibi_broken", "ms-pike", "testimony", 0.9) // no motive: not a suspect
	add("motive_known", "mr-oates", "tip", 0.8)      // alibi holds: not a suspect
	add("alibi_broken", "mr-oates", "hunch", 0.9)    // a hunch does not count as a broken alibi
	add("case_open", "case-17", "testimony", 1)

	engine, err := infer.New(infer.DefaultConfig(), []infer.Rule{{
		ID: "person-of-interest",
		Requires: []infer.Requirement{
			{Type: "alibi_broken", Sources: []string{"testimony"}},
			{Type: "motive_known"},
			{Type: "case_open", Subject: "case-17"},
		},
		Produces:   infer.Product{Type: "person_of_interest", Private: true},
		Confidence: 0.8,
	}})
	if err != nil {
		fmt.Println(err)
		return
	}
	res, _ := engine.Run(store, 0)
	for _, d := range res.Derived {
		f, _ := store.Get(d.ID)
		fmt.Println(f.Subject, f.Confidence, "private:", f.Private, "premises:", len(f.Prov.Premises))
	}

	// Output:
	// mr-vane 0.8 private: true premises: 3
}

func ptr[T any](v T) *T { return &v }
