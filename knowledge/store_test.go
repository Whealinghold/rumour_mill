package knowledge

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestAddValidation(t *testing.T) {
	tests := []struct {
		name string
		f    Fact
		want error
	}{
		{"ok", Fact{Type: "t", Source: "observed", Confidence: 0.5}, nil},
		{"empty type", Fact{Source: "observed"}, ErrInvalidFact},
		{"empty source", Fact{Type: "t"}, ErrInvalidFact},
		{"nan confidence", Fact{Type: "t", Source: "observed", Confidence: nan()}, ErrInvalidFact},
		{"separator in type", Fact{Type: "a\x1fb", Source: "observed"}, ErrInvalidFact},
		{"separator in subject", Fact{Type: "t", Subject: "a\x1fb", Source: "observed"}, ErrInvalidFact},
		{"separator in discriminator", Fact{Type: "t", Discriminator: "\x1f", Source: "observed"}, ErrInvalidFact},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := mustStore(t, DefaultConfig())
			_, err := s.Add(tc.f)
			if !errors.Is(err, tc.want) && !(err == nil && tc.want == nil) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if tc.want != nil && len(s.All()) != 0 {
				t.Error("invalid fact was stored")
			}
		})
	}
}

func TestAddClampsConfidence(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{{-3, 0}, {0.4, 0.4}, {7, 1}} {
		s := mustStore(t, DefaultConfig())
		res, _ := s.Add(Fact{Type: "t", Source: "observed", Confidence: tc.in})
		if got, _ := s.Get(res.ID); got.Confidence != tc.want {
			t.Errorf("confidence %v stored as %v, want %v", tc.in, got.Confidence, tc.want)
		}
	}
}

func TestIdentity(t *testing.T) {
	s := mustStore(t, DefaultConfig())
	a, _ := s.Add(Fact{Type: "t", Subject: "x", Source: "observed", Confidence: 0.5})
	b, _ := s.Add(Fact{Type: "t", Subject: "x", Source: "observed", Confidence: 0.9})
	c, _ := s.Add(Fact{Type: "t", Subject: "x", Discriminator: "event-2", Source: "observed", Confidence: 0.5})
	d, _ := s.Add(Fact{Type: "t", Subject: "y", Source: "observed", Confidence: 0.5})
	if a.ID != b.ID || b.Outcome != Replaced {
		t.Errorf("same claim should merge: %+v %+v", a, b)
	}
	if c.ID == a.ID || d.ID == a.ID || c.ID == d.ID {
		t.Errorf("discriminator and subject must separate identities: %q %q %q", a.ID, c.ID, d.ID)
	}
	if n := len(s.All()); n != 3 {
		t.Errorf("stored %d facts, want 3", n)
	}
}

func TestIdentityMismatch(t *testing.T) {
	s := mustStore(t, DefaultConfig())
	s.Add(Fact{ID: "k", Type: "a", Subject: "x", Source: "observed", Confidence: 0.5})
	for _, f := range []Fact{
		{ID: "k", Type: "b", Subject: "x", Source: "observed", Confidence: 0.9},
		{ID: "k", Type: "a", Subject: "y", Source: "observed", Confidence: 0.9},
	} {
		if _, err := s.Add(f); !errors.Is(err, ErrIdentityMismatch) {
			t.Errorf("Add(%+v) err = %v, want ErrIdentityMismatch", f, err)
		}
	}
	if got, _ := s.Get("k"); got.Confidence != 0.5 {
		t.Error("mismatching add changed the stored fact")
	}
}

func TestDefaultMergePolicy(t *testing.T) {
	tests := []struct {
		name      string
		old, inc  Fact
		wantOut   Outcome
		wantFinal float64
	}{
		{"higher rank wins despite lower confidence", fact("relayed", 0.9), fact("observed", 0.3), Replaced, 0.3},
		{"lower rank loses despite higher confidence", fact("observed", 0.3), fact("relayed", 0.9), Rejected, 0.3},
		{"equal rank higher confidence wins", fact("relayed", 0.5), fact("relayed", 0.6), Replaced, 0.6},
		{"equal rank lower confidence loses", fact("relayed", 0.5), fact("relayed", 0.4), Rejected, 0.5},
		{"full tie keeps existing (idempotent)", fact("relayed", 0.5), fact("relayed", 0.5), Rejected, 0.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := mustStore(t, DefaultConfig())
			s.Add(tc.old)
			res, _ := s.Add(tc.inc)
			if res.Outcome != tc.wantOut {
				t.Errorf("outcome = %v, want %v", res.Outcome, tc.wantOut)
			}
			if got := s.All()[0].Confidence; got != tc.wantFinal {
				t.Errorf("final confidence = %v, want %v", got, tc.wantFinal)
			}
		})
	}
}

func TestReplaceOverwritesWholeFact(t *testing.T) {
	s := mustStore(t, DefaultConfig())
	exp := Tick(50)
	s.Add(Fact{Type: "t", Subject: "s", Source: "relayed", Confidence: 0.5, Expiry: &exp, Payload: "old",
		Prov: Provenance{From: "ann", Hops: 2}})
	s.Add(Fact{Type: "t", Subject: "s", Source: "observed", Confidence: 0.5, Observed: 9, Payload: "new"})
	got := s.All()[0]
	if got.Expiry != nil || got.Payload != "new" || got.Prov.From != "" || got.Observed != 9 {
		t.Errorf("replace should overwrite everything, got %+v", got)
	}
}

func TestCustomMergePolicy(t *testing.T) {
	newest := func(existing, incoming Fact, _ func(string) int) Decision {
		if incoming.Observed > existing.Observed {
			return Replace
		}
		return Keep
	}
	s := mustStore(t, DefaultConfig(), WithMergePolicy(newest))
	s.Add(Fact{Type: "t", Source: "observed", Confidence: 0.9, Observed: 1})
	res, _ := s.Add(Fact{Type: "t", Source: "derived", Confidence: 0.1, Observed: 2})
	if res.Outcome != Replaced {
		t.Errorf("custom policy ignored: %v", res.Outcome)
	}
	// A nil policy keeps the default.
	s = mustStore(t, DefaultConfig(), WithMergePolicy(nil))
	s.Add(fact("observed", 0.9))
	if res, _ := s.Add(fact("derived", 0.1)); res.Outcome != Rejected {
		t.Errorf("nil policy should keep the default, got %v", res.Outcome)
	}
}

func TestExpiry(t *testing.T) {
	s := mustStore(t, DefaultConfig())
	exp := Tick(100)
	s.Add(Fact{Type: "t", Subject: "mortal", Source: "observed", Confidence: 0.5, Expiry: &exp})
	s.Add(Fact{Type: "t", Subject: "forever", Source: "observed", Confidence: 0.5})
	for _, tc := range []struct {
		now  Tick
		want int
	}{{0, 2}, {99, 2}, {100, 1}, {1000, 1}} {
		if got := len(s.Find(Query{}, tc.now)); got != tc.want {
			t.Errorf("Find at %d = %d facts, want %d", tc.now, got, tc.want)
		}
	}
	if got, ok := s.Get("t\x1fmortal\x1f"); !ok || got.Subject != "mortal" {
		t.Error("Get should return expired facts")
	}
	if n := s.Prune(99); n != 0 {
		t.Errorf("Prune(99) = %d, want 0", n)
	}
	if n := s.Prune(100); n != 1 {
		t.Errorf("Prune(100) = %d, want 1", n)
	}
	if n := len(s.All()); n != 1 {
		t.Errorf("after prune %d facts remain, want 1", n)
	}
}

func TestFind(t *testing.T) {
	s := mustStore(t, DefaultConfig())
	for _, f := range []Fact{
		{ID: "1", Type: "a", Subject: "x", Source: "observed", Confidence: 0.9},
		{ID: "2", Type: "a", Subject: "y", Source: "relayed", Confidence: 0.5},
		{ID: "3", Type: "b", Subject: "x", Source: "relayed", Confidence: 0.1},
		{ID: "4", Type: "b", Subject: "y", Source: "derived", Confidence: 0.6},
	} {
		if _, err := s.Add(f); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		q    Query
		want []string
	}{
		{"all above default floor", Query{}, []string{"1", "2", "4"}},
		{"by type", Query{Type: "a"}, []string{"1", "2"}},
		{"by subject", Query{Subject: "y"}, []string{"2", "4"}},
		{"type and subject", Query{Type: "b", Subject: "y"}, []string{"4"}},
		{"by sources", Query{Sources: []string{"relayed", "derived"}}, []string{"2", "4"}},
		{"explicit floor", Query{MinConfidence: ptr(0.55)}, []string{"1", "4"}},
		{"floor zero reveals weak facts", Query{MinConfidence: ptr(0.0)}, []string{"1", "2", "3", "4"}},
		{"case sensitive", Query{Type: "A"}, nil},
		{"no match", Query{Type: "zzz"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, f := range s.Find(tc.q, 0) {
				got = append(got, f.ID)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvictionOrder(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxFacts = 2
	tests := []struct {
		name    string
		adds    []Fact
		evicted string
		kept    []string
	}{
		{"lowest rank first",
			[]Fact{{ID: "a", Source: "observed", Confidence: 0.1}, {ID: "b", Source: "derived", Confidence: 0.9}, {ID: "c", Source: "relayed", Confidence: 0.5}},
			"b", []string{"a", "c"}},
		{"then lowest confidence",
			[]Fact{{ID: "a", Source: "relayed", Confidence: 0.5}, {ID: "b", Source: "relayed", Confidence: 0.3}, {ID: "c", Source: "relayed", Confidence: 0.4}},
			"b", []string{"a", "c"}},
		{"then oldest observed",
			[]Fact{{ID: "a", Source: "relayed", Confidence: 0.5, Observed: 5}, {ID: "b", Source: "relayed", Confidence: 0.5, Observed: 1}, {ID: "c", Source: "relayed", Confidence: 0.5, Observed: 9}},
			"b", []string{"a", "c"}},
		{"then smallest ID",
			[]Fact{{ID: "b", Source: "relayed", Confidence: 0.5}, {ID: "a", Source: "relayed", Confidence: 0.5}, {ID: "c", Source: "relayed", Confidence: 0.5}},
			"a", []string{"b", "c"}},
		{"incoming fact can be the weakest",
			[]Fact{{ID: "a", Source: "observed", Confidence: 0.9}, {ID: "b", Source: "observed", Confidence: 0.8}, {ID: "c", Source: "derived", Confidence: 0.1}},
			"c", []string{"a", "b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := mustStore(t, cfg)
			var last AddResult
			for _, f := range tc.adds {
				f.Type = "t"
				var err error
				if last, err = s.Add(f); err != nil {
					t.Fatal(err)
				}
			}
			if !slices.Equal(last.Evicted, []string{tc.evicted}) {
				t.Errorf("evicted %v, want [%s]", last.Evicted, tc.evicted)
			}
			var kept []string
			for _, f := range s.All() {
				kept = append(kept, f.ID)
			}
			if !slices.Equal(kept, tc.kept) {
				t.Errorf("kept %v, want %v", kept, tc.kept)
			}
		})
	}
}

func TestEvictedIncomingIsRejected(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxFacts = 1
	s := mustStore(t, cfg)
	s.Add(Fact{ID: "a", Type: "t", Source: "observed", Confidence: 0.9})
	res, _ := s.Add(Fact{ID: "b", Type: "t", Source: "derived", Confidence: 0.1})
	if res.Outcome != Rejected || !slices.Equal(res.Evicted, []string{"b"}) {
		t.Errorf("got %+v, want Rejected with b evicted", res)
	}
	if _, ok := s.Get("b"); ok {
		t.Error("evicted incoming fact is still stored")
	}
}

func TestResultsAreCopies(t *testing.T) {
	s := mustStore(t, DefaultConfig())
	exp := Tick(500)
	in := Fact{Type: "t", Source: "observed", Confidence: 0.5, Expiry: &exp, Prov: Provenance{Premises: []string{"p1"}}}
	s.Add(in)
	exp = 1 // caller mutates after Add
	in.Prov.Premises[0] = "tampered"

	got := s.All()[0]
	if *got.Expiry != 500 || got.Prov.Premises[0] != "p1" {
		t.Fatalf("store aliased caller data: %+v", got)
	}
	*got.Expiry = 2
	got.Prov.Premises[0] = "tampered"
	again, _ := s.Get(got.ID)
	if *again.Expiry != 500 || again.Prov.Premises[0] != "p1" {
		t.Error("store returned aliased data")
	}
	found := s.Find(Query{}, 0)[0]
	*found.Expiry = 3
	if again, _ = s.Get(got.ID); *again.Expiry != 500 {
		t.Error("Find returned aliased data")
	}
}

func TestOrderingIsDeterministic(t *testing.T) {
	build := func(order []string) []Fact {
		s := mustStore(t, DefaultConfig())
		for _, id := range order {
			s.Add(Fact{ID: id, Type: "t", Source: "observed", Confidence: 0.5})
		}
		return s.All()
	}
	a := build([]string{"c", "a", "b", "e", "d"})
	b := build([]string{"d", "e", "b", "a", "c"})
	if !reflect.DeepEqual(a, b) {
		t.Errorf("order depends on insertion order:\n%v\n%v", a, b)
	}
	if ids := []string{a[0].ID, a[1].ID, a[2].ID, a[3].ID, a[4].ID}; !slices.IsSorted(ids) {
		t.Errorf("All not sorted by ID: %v", ids)
	}
}

func TestOutcomeString(t *testing.T) {
	for o, want := range map[Outcome]string{Rejected: "rejected", Inserted: "inserted", Replaced: "replaced", 9: "unknown"} {
		if o.String() != want {
			t.Errorf("Outcome(%d).String() = %q, want %q", int(o), o.String(), want)
		}
	}
}
