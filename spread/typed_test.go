package spread_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/logic"
	"github.com/Whealinghold/rumour_mill/spread"
)

// Office clerks only pass notices to colleagues on the same floor.
func Example_typedFilter() {
	cfg := knowledge.DefaultConfig()
	ana, bo, cy := newPerson("ana", cfg), newPerson("bo", cfg), newPerson("cy", cfg)
	ana.group, bo.group, cy.group = "floor-1", "floor-1", "floor-2"
	ana.kb.Add(knowledge.Fact{ID: "printer-broken", Type: "notice", Subject: "printer", Confidence: 0.9, Source: "witnessed"})

	sameFloor := spread.Typed(func(e spread.TypedEncounter[*person]) bool {
		return e.Teller.group == e.Listener.group
	})
	sp, _ := spread.New(spread.DefaultConfig(), spread.WithFilter(sameFloor))
	res, _ := sp.Spread(ana, holders(bo, cy), 1)
	for _, t := range res.Transfers {
		fmt.Println("ana ->", t.To)
	}
	fmt.Println("filtered:", res.Dropped.Filtered)

	// Output:
	// ana -> bo
	// filtered: 1
}

type nilHolder struct{}

func (nilHolder) ID() string                  { return "x" }
func (nilHolder) Knowledge() *knowledge.Store { return nil }

func TestTyped(t *testing.T) {
	p, q := &person{id: "p"}, &person{id: "q"}
	other := nilHolder{}
	tests := []struct {
		name         string
		teller, lstn spread.Holder
		want, called bool
	}{
		{"match", p, q, true, true},
		{"wrong teller", other, q, false, false},
		{"wrong listener", p, other, false, false},
		{"nil teller", nil, q, false, false},
		{"nil listener", p, nil, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := spread.Typed(func(spread.TypedEncounter[*person]) bool { calls++; return true })
			got := c(spread.Encounter{Teller: tc.teller, Listener: tc.lstn})
			if got != tc.want || (calls == 1) != tc.called {
				t.Fatalf("got %v calls %d", got, calls)
			}
		})
	}
}

func TestTypedInterfaceH(t *testing.T) {
	c := spread.Typed(func(e spread.TypedEncounter[spread.Holder]) bool { return e.Teller.ID() == "p" })
	if !c(spread.Encounter{Teller: &person{id: "p"}, Listener: nilHolder{}}) {
		t.Fatal("interface H should accept any non-nil holder")
	}
	if c(spread.Encounter{Teller: nil, Listener: nilHolder{}}) {
		t.Fatal("nil holder must fail")
	}
}

func TestTypedFieldsAndNil(t *testing.T) {
	var got spread.TypedEncounter[*person]
	c := spread.Typed(func(e spread.TypedEncounter[*person]) bool { got = e; return true })
	p := &person{id: "p"}
	c(spread.Encounter{Teller: p, Listener: p, Fact: knowledge.Fact{ID: "f"}, Now: 7})
	if got.Fact.ID != "f" || got.Now != 7 || got.Teller != p {
		t.Fatalf("fields not passed: %+v", got)
	}
	if spread.Typed[*person](nil) != nil {
		t.Fatal("nil f must give nil condition")
	}
	if _, err := spread.New(spread.DefaultConfig(), spread.WithFilter(spread.Typed[*person](nil))); err != nil {
		t.Fatal(err)
	}
}

func TestTypedComposes(t *testing.T) {
	p := &person{id: "p"}
	yes := spread.Typed(func(spread.TypedEncounter[*person]) bool { return true })
	e := spread.Encounter{Teller: p, Listener: p}
	if logic.Not(yes)(e) || !logic.All(yes, yes)(e) {
		t.Fatal("composition broken")
	}
	// Typed fails closed on a mismatch (returns false), so Not over it returns
	// true by design: the fail-closed guarantee covers Typed itself, not
	// combinators wrapped around it.
	if !logic.Not(yes)(spread.Encounter{Teller: nilHolder{}, Listener: p}) {
		t.Fatal("Not over a blocked encounter should be true")
	}
}

func TestTypedEncounterMirrorsEncounter(t *testing.T) {
	et, tt := reflect.TypeOf(spread.Encounter{}), reflect.TypeOf(spread.TypedEncounter[*person]{})
	holder := reflect.TypeOf((*spread.Holder)(nil)).Elem()
	for i := 0; i < et.NumField(); i++ {
		f := et.Field(i)
		if !f.IsExported() {
			continue // internal plumbing, deliberately not mirrored
		}
		g, ok := tt.FieldByName(f.Name)
		if !ok {
			t.Fatalf("TypedEncounter lacks field %s", f.Name)
		}
		if f.Type == holder {
			if g.Type != reflect.TypeOf(&person{}) {
				t.Fatalf("%s should be H", f.Name)
			}
		} else if g.Type != f.Type {
			t.Fatalf("%s type differs", f.Name)
		}
	}
}

// Gossip passed between different floors loses some confidence.
func Example_typedTransform() {
	cfg := knowledge.DefaultConfig()
	ana, bo, cy := newPerson("ana", cfg), newPerson("bo", cfg), newPerson("cy", cfg)
	ana.group, bo.group, cy.group = "floor-1", "floor-1", "floor-2"
	ana.kb.Add(knowledge.Fact{ID: "printer-broken", Type: "notice", Subject: "printer", Confidence: 0.8, Source: "witnessed"})

	sp, _ := spread.New(spread.DefaultConfig())
	sp.Transform = spread.TypedTransform(func(e spread.TypedEncounter[*person], d knowledge.Fact) knowledge.Fact {
		if e.Teller.group != e.Listener.group {
			d.Confidence /= 2
		}
		return d
	})
	sp.Spread(ana, holders(bo, cy), 1)
	for _, p := range []*person{bo, cy} {
		f, _ := p.kb.Get("printer-broken")
		fmt.Printf("%s %.2f\n", p.id, f.Confidence)
	}

	// Output:
	// bo 0.64
	// cy 0.32
}

func TestTypedTransform(t *testing.T) {
	p, q := &person{id: "p"}, &person{id: "q"}
	del := knowledge.Fact{ID: "d", Confidence: 0.5}
	tests := []struct {
		name         string
		teller, lstn spread.Holder
		called       bool
	}{
		{"match", p, q, true},
		{"wrong teller", nilHolder{}, q, false},
		{"wrong listener", p, nilHolder{}, false},
		{"nil teller", nil, q, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var tr spread.Transform = spread.TypedTransform(func(e spread.TypedEncounter[*person], d knowledge.Fact) knowledge.Fact {
				calls++
				d.Confidence = 0.1
				return d
			})
			got := tr(spread.Encounter{Teller: tc.teller, Listener: tc.lstn}, del)
			if (calls == 1) != tc.called || (got.Confidence == 0.1) != tc.called {
				t.Fatalf("calls %d got %+v", calls, got)
			}
		})
	}
}

func TestTypedTransformNilAndFields(t *testing.T) {
	if spread.TypedTransform[*person](nil) != nil {
		t.Fatal("nil f must give nil")
	}
	p := &person{id: "p"}
	var got spread.TypedEncounter[*person]
	tr := spread.TypedTransform(func(e spread.TypedEncounter[*person], d knowledge.Fact) knowledge.Fact { got = e; return d })
	tr(spread.Encounter{Teller: p, Listener: p, Fact: knowledge.Fact{ID: "f"}, Now: 9}, knowledge.Fact{})
	if got.Fact.ID != "f" || got.Now != 9 || got.Teller != p {
		t.Fatalf("fields not passed: %+v", got)
	}
}

func TestTypedTransformFixedFieldsRestored(t *testing.T) {
	cfg := knowledge.DefaultConfig()
	ana, bo := newPerson("ana", cfg), newPerson("bo", cfg)
	ana.kb.Add(knowledge.Fact{ID: "f", Type: "notice", Subject: "s", Confidence: 0.9, Source: "witnessed"})
	sp, _ := spread.New(spread.DefaultConfig())
	sp.Transform = spread.TypedTransform(func(e spread.TypedEncounter[*person], d knowledge.Fact) knowledge.Fact {
		d.ID, d.Type = "forged", "other"
		return d
	})
	if _, err := sp.Spread(ana, holders(bo), 1); err != nil {
		t.Fatal(err)
	}
	f, ok := bo.kb.Get("f")
	if !ok || f.Type != "notice" {
		t.Fatalf("ID/Type not restored: %+v ok=%v", f, ok)
	}
}

// A listener of another Holder type is blocked and counted as filtered; the
// filter body never sees it and nothing panics.
func TestTypedFilterEndToEnd(t *testing.T) {
	cfg := knowledge.DefaultConfig()
	ana, bo := newPerson("ana", cfg), newPerson("bo", cfg)
	kb, _ := knowledge.NewStore(cfg)
	stranger := otherHolder{kb}
	ana.kb.Add(knowledge.Fact{ID: "f", Type: "notice", Subject: "s", Confidence: 0.9, Source: "witnessed"})
	sp, err := spread.New(spread.DefaultConfig(), spread.WithFilter(spread.Typed(func(spread.TypedEncounter[*person]) bool { return true })))
	if err != nil {
		t.Fatal(err)
	}
	res, err := sp.Spread(ana, []spread.Holder{stranger, bo}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Dropped.Filtered != 1 || len(res.Transfers) != 1 || res.Transfers[0].To != "bo" {
		t.Fatalf("filtered %d transfers %+v", res.Dropped.Filtered, res.Transfers)
	}
	if _, ok := bo.kb.Get("f"); !ok {
		t.Fatal("matching listener should receive the fact")
	}
	if len(kb.All()) != 0 {
		t.Fatal("filtered listener must receive nothing")
	}
}

// otherHolder is a Holder with a real store but not a *person.
type otherHolder struct{ kb *knowledge.Store }

func (otherHolder) ID() string                    { return "stranger" }
func (o otherHolder) Knowledge() *knowledge.Store { return o.kb }

func TestOnMismatchAndHooks(t *testing.T) {
	p := &person{id: "p"}
	mismatches := 0
	opt := spread.OnMismatch(func(spread.Encounter) { mismatches++ })

	f := spread.Typed(func(spread.TypedEncounter[*person]) bool { return true }, opt)
	f(spread.Encounter{Teller: nilHolder{}, Listener: p})
	f(spread.Encounter{Teller: p, Listener: p})
	tr := spread.TypedTransform(func(_ spread.TypedEncounter[*person], d knowledge.Fact) knowledge.Fact { return d }, opt)
	tr(spread.Encounter{Teller: p, Listener: nilHolder{}}, knowledge.Fact{})
	if mismatches != 2 {
		t.Fatalf("mismatches %d, want 2 (one Typed, one TypedTransform)", mismatches)
	}

	cfg := knowledge.DefaultConfig()
	ana, bo := newPerson("ana", cfg), newPerson("bo", cfg)
	ana.kb.Add(knowledge.Fact{ID: "x", Type: "notice", Subject: "s", Confidence: 0.9, Source: "witnessed"})
	var got []spread.Result
	sp, _ := spread.New(spread.DefaultConfig(), spread.WithHooks(spread.Hooks{OnSpread: func(r spread.Result, _ error) { got = append(got, r) }}))
	sp.Spread(ana, holders(bo), 1)
	if len(got) != 1 || len(got[0].Transfers) != 1 {
		t.Fatalf("OnSpread results %+v", got)
	}
}

func TestDroppedEachAndChainedHooks(t *testing.T) {
	var got []string
	spread.Dropped{Filtered: 1, BackToSource: 2, BelowMinConfidence: 3, TransformDropped: 4}.Each(func(r string, n int) {
		got = append(got, fmt.Sprint(r, "=", n))
	})
	if want := "[filtered=1 back_to_source=2 below_min_confidence=3 transform_dropped=4]"; fmt.Sprint(got) != want {
		t.Errorf("Each = %v, want %s", got, want)
	}

	var order []string
	sp, _ := spread.New(spread.DefaultConfig(),
		spread.WithHooks(spread.Hooks{OnSpread: func(spread.Result, error) { order = append(order, "a") }}),
		spread.WithHooks(spread.Hooks{OnSpread: func(spread.Result, error) { order = append(order, "b") }}),
		spread.WithHooks(spread.Hooks{}))
	cfg := knowledge.DefaultConfig()
	sp.Spread(newPerson("ana", cfg), holders(newPerson("bo", cfg)), 1)
	if fmt.Sprint(order) != "[a b]" {
		t.Errorf("hooks ran %v, want [a b]", order)
	}

	n := 0
	p := &person{id: "p"}
	f := spread.Typed(func(spread.TypedEncounter[*person]) bool { return true },
		spread.OnMismatch(func(spread.Encounter) { n++ }), spread.OnMismatch(func(spread.Encounter) { n += 10 }))
	f(spread.Encounter{Teller: nilHolder{}, Listener: p})
	if n != 11 {
		t.Errorf("mismatch hooks gave %d, want 11", n)
	}
}
