package declare_test

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/spread"
)

const fadeDoc = `{"version": 1, "config": {"knowledge": {"fade": {"half_lives": {"overheard": 100}, "forget_below": 0.15}}}}`

// S14 (R23, R27): a data file configures fading; behaviour matches the S12 numbers.
func TestLoadFadeConfigDrivesFade(t *testing.T) {
	doc, err := loadString(fadeDoc)
	if err != nil {
		t.Fatal(err)
	}
	store, err := knowledge.NewStore(doc.Config.Knowledge)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []knowledge.Fact{
		{ID: "a", Type: "t", Subject: "a", Source: "overheard", Confidence: 0.8},
		{ID: "b", Type: "t", Subject: "b", Source: "observed", Confidence: 0.8},
	} {
		if _, err := store.Add(f); err != nil {
			t.Fatal(err)
		}
	}
	steps := []struct {
		now       knowledge.Tick
		want      float64
		forgotten bool
	}{{100, 0.4, false}, {200, 0.2, false}, {300, 0, true}}
	for _, st := range steps {
		store.Fade(st.now)
		got, ok := store.Get("a")
		if st.forgotten {
			if ok {
				t.Errorf("tick %d: fact should be forgotten", st.now)
			}
			continue
		}
		if !ok || math.Abs(got.Confidence-st.want) > 1e-9 {
			t.Errorf("tick %d: confidence = %v (ok=%v), want %v", st.now, got.Confidence, ok, st.want)
		}
	}
	if b, _ := store.Get("b"); b.Confidence != 0.8 {
		t.Errorf("unlisted source faded: %v", b.Confidence)
	}
}

func TestLoadFadeErrorsAndDefaults(t *testing.T) {
	if _, err := loadString(`{"version": 1, "config": {"knowledge": {"fade": {"half_live": {"x": 1}}}}}`); err == nil {
		t.Error("typo key knowledge.fade.half_live must fail")
	}
	bad, err := loadString(`{"version": 1, "config": {"knowledge": {"fade": {"forget_below": 2}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.Config.Validate(); err == nil || !strings.Contains(err.Error(), "forget_below") {
		t.Errorf("err = %v, want forget_below validation error", err)
	}
	doc, err := loadString(`{"version": 1}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := doc.Config.Knowledge.Fade, knowledge.DefaultConfig().Fade; !reflect.DeepEqual(got, want) { // DeepEqual tells nil from empty map
		t.Errorf("Fade = %+v, want defaults", got)
	}
}

func TestDefaultFadeHalfLivesAreFresh(t *testing.T) {
	a, b := knowledge.DefaultConfig(), knowledge.DefaultConfig()
	a.Fade.HalfLives["overheard"] = 1
	if len(b.Fade.HalfLives) != 0 {
		t.Errorf("second DefaultConfig shares HalfLives: %v", b.Fade.HalfLives)
	}
	doc, err := loadString(`{"version": 1}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Config.Knowledge.Fade.HalfLives) != 0 {
		t.Errorf("loaded config shares HalfLives with a DefaultConfig caller: %v", doc.Config.Knowledge.Fade.HalfLives)
	}
}

// End to end: a data file configures fading, spreading and inference; a rumour
// is told, believed, inferred from, then fades from memory so inference stops
// firing on it. The control run without fade config keeps believing it.
func TestFadeEndToEndRumourLifecycle(t *testing.T) {
	const doc = `{"version": 1, "config": {
		"knowledge": {"fade": %s},
		"spread": {"decay_factor": 0.85, "received_kind": "corridor_talk"},
		"infer": {"derived_kind": "clinical_guess"}},
	 "inference": [{"id": "watch", "requires": [{"type": "vitals", "min_confidence": 0.5}, {"type": "labs"}],
		"produces": {"type": "risk", "ttl": 24}, "confidence": 0.6}]}`
	for _, tc := range []struct {
		name, fade string
		wantKnown  bool
		wantDerive int
	}{
		{"fade on", `{"half_lives": {"corridor_talk": 50}, "forget_below": 0.1}`, false, 0},
		{"fade off", `{}`, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := loadString(fmt.Sprintf(doc, tc.fade))
			if err != nil {
				t.Fatal(err)
			}
			hire := func(id string) *staff {
				kb, err := knowledge.NewStore(d.Config.Knowledge)
				if err != nil {
					t.Fatal(err)
				}
				return &staff{id: id, kb: kb}
			}
			alice, bo := hire("alice"), hire("bo")
			sp, _ := spread.New(d.Config.Spread)
			eng, _ := infer.New(d.Config.Infer, d.Inference)
			alice.kb.Add(knowledge.Fact{Type: "vitals", Subject: "bed-7", Confidence: 0.9, Source: "charted"})
			bo.kb.Add(knowledge.Fact{Type: "labs", Subject: "bed-7", Confidence: 0.9, Source: "charted"})

			sp.Spread(alice, []spread.Holder{bo}, 10)
			if res, _ := eng.Run(bo.kb, 10); len(res.Derived) != 1 {
				t.Fatalf("t=10 derived %d, want 1 (rumour should be believed on arrival)", len(res.Derived))
			}
			bo.kb.Prune(200) // drop the expired derivation so inference may re-fire
			bo.kb.Fade(200)  // ~4 half-lives: 0.765 -> ~0.05
			known := len(bo.kb.Find(knowledge.Query{Type: "vitals", Sources: []string{"corridor_talk"}}, 200)) > 0
			res, _ := eng.Run(bo.kb, 200) // re-derives only if the rumour is still believed
			if known != tc.wantKnown || len(res.Derived) != tc.wantDerive {
				t.Errorf("t=200 rumour known=%v derived=%d, want known=%v derived=%d", known, len(res.Derived), tc.wantKnown, tc.wantDerive)
			}
		})
	}
}
