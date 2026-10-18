package declare_test

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/Whealinghold/rumour_mill/declare"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/rules"
)

func loadString(s string) (declare.Document, error) { return declare.Load(strings.NewReader(s), nil) }

func TestDefaultConfigIsTheSumOfItsParts(t *testing.T) {
	c := declare.DefaultConfig()
	if !reflect.DeepEqual(c.Knowledge, knowledge.DefaultConfig()) || c.Rules.DefaultPriority != 100 || c.Rules.OnActionError != "continue" ||
		c.Spread.DecayFactor != 0.8 || c.Infer.MaxPasses != 1 {
		t.Errorf("DefaultConfig() = %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("default config does not validate: %v", err)
	}
	c.Knowledge.SourceRanks["observed"] = 99
	if declare.DefaultConfig().Knowledge.SourceRanks["observed"] != 3 {
		t.Error("DefaultConfig must return a fresh copy each time")
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*declare.Config)
		want   string // substring expected in the error; "" = valid
	}{
		{"default", func(*declare.Config) {}, ""},
		{"knowledge section", func(c *declare.Config) { c.Knowledge.QueryFloor = 2 }, "query_floor"},
		{"rules section", func(c *declare.Config) { c.Rules.OnActionError = "ignore" }, "on_action_error"},
		{"spread section", func(c *declare.Config) { c.Spread.DecayFactor = 0 }, "decay_factor"},
		{"infer section", func(c *declare.Config) { c.Infer.MaxPasses = 0 }, "max_passes"},
		{"received kind unranked", func(c *declare.Config) { c.Spread.ReceivedKind = "hearsay" }, `spread.received_kind "hearsay"`},
		{"derived kind unranked", func(c *declare.Config) { c.Infer.DerivedKind = "deduced" }, `infer.derived_kind "deduced"`},
		{"custom ranks that include both kinds", func(c *declare.Config) {
			c.Knowledge.SourceRanks = map[string]int{"seen": 3, "told": 2, "guessed": 1}
			c.Spread.ReceivedKind, c.Infer.DerivedKind = "told", "guessed"
		}, ""},
		{"replacing ranks without updating kinds", func(c *declare.Config) { c.Knowledge.SourceRanks = map[string]int{"seen": 3} }, "not in knowledge.source_ranks"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := declare.DefaultConfig()
			tc.mutate(&c)
			err := c.Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tc.want != "" && (!errors.Is(err, declare.ErrInvalidConfig) || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("err = %v, want ErrInvalidConfig mentioning %q", err, tc.want)
			}
		})
	}
	// Problems are reported together.
	c := declare.DefaultConfig()
	c.Spread.DecayFactor, c.Infer.MaxPasses = 0, 0
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "decay_factor") || !strings.Contains(err.Error(), "max_passes") {
		t.Errorf("err = %v, want both", err)
	}
}

func TestLoadOverlay(t *testing.T) {
	doc, err := loadString(`{
		"version": 1,
		"config": {
			"knowledge": {"query_floor": 0.5, "max_facts": 0},
			"rules": {"default_priority": 7},
			"spread": {"max_hops": 3}
		}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	want := declare.DefaultConfig()
	want.Knowledge.QueryFloor = 0.5
	want.Rules.DefaultPriority = 7
	want.Spread.MaxHops = 3
	if !reflect.DeepEqual(doc.Config, want) {
		t.Errorf("config = %+v\nwant   %+v", doc.Config, want)
	}
}

func TestLoadExplicitZeroBeatsDefault(t *testing.T) {
	doc, err := loadString(`{"version":1,"config":{"knowledge":{"query_floor":0},"spread":{"skip_back_to_source":false}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Config.Knowledge.QueryFloor != 0 || doc.Config.Spread.SkipBackToSource {
		t.Errorf("an explicit zero/false must replace the default: %+v", doc.Config)
	}
}

func TestLoadMapsReplace(t *testing.T) {
	tests := []struct {
		name, ranks string
		want        map[string]int
	}{
		{"absent keeps the default map", ``, knowledge.DefaultConfig().SourceRanks},
		{"present replaces it, no merging", `"source_ranks": {"seen": 2},`, map[string]int{"seen": 2}},
		{"empty replaces with empty", `"source_ranks": {},`, map[string]int{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := loadString(`{"version":1,"config":{"knowledge":{` + tc.ranks + `"unranked_rank":1}}}`)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(doc.Config.Knowledge.SourceRanks, tc.want) {
				t.Errorf("source_ranks = %v, want %v", doc.Config.Knowledge.SourceRanks, tc.want)
			}
		})
	}
}

func TestLoadRulesConfigSharesOneSection(t *testing.T) {
	doc, err := loadString(`{"version":1,"config":{"rules":{"on_action_error":"stop","default_priority":3}}}`)
	if err != nil || doc.Config.Rules.OnActionError != "stop" || doc.Config.Rules.DefaultPriority != 3 {
		t.Errorf("rules section: %+v %v", doc.Config.Rules, err)
	}
	if _, err := loadString(`{"version":1,"config":{"rules":{"nope":1}}}`); err == nil {
		t.Error("unknown key in the embedded section should be rejected")
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name, in string
		want     error
		contains string
	}{
		{"missing version", `{}`, declare.ErrUnsupportedVersion, "got 0"},
		{"wrong version", `{"version": 2}`, declare.ErrUnsupportedVersion, "got 2"},
		{"unknown top-level key", `{"version":1,"rulez":[]}`, nil, "rulez"},
		{"unknown config key", `{"version":1,"config":{"spread":{"decay":0.5}}}`, nil, "decay"},
		{"unknown rule key", `{"version":1,"rules":[{"id":"a","layer":"u","event":"e","priorty":1}]}`, nil, "priorty"},
		{"unknown node key", `{"version":1,"rules":[{"id":"a","layer":"u","event":"e","when":{"use":"x","arg":{}}}]}`, nil, "arg"},
		{"unknown inference key", `{"version":1,"inference":[{"id":"a","confidence":0.5,"extra":1}]}`, nil, "extra"},
		{"wrong type", `{"version":1,"config":{"spread":{"max_hops":"many"}}}`, nil, "max_hops"},
		{"not an integer", `{"version":1,"config":{"spread":{"max_hops":1.5}}}`, nil, "max_hops"},
		{"syntax error", `{"version":1,`, nil, "unexpected EOF"},
		{"empty input", ``, nil, "EOF"},
		{"trailing data", `{"version":1} {"version":1}`, nil, "after the JSON document"},
		{"not an object", `[1,2]`, nil, "array"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(tc.in)
			if err == nil {
				t.Fatal("expected an error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("err = %v, want it to mention %q", err, tc.contains)
			}
		})
	}
}

func TestLoadUsesTheGivenDecoder(t *testing.T) {
	called := false
	doc, err := declare.Load(strings.NewReader("ignored"), func(r io.Reader, v any) error {
		called = true
		d := v.(*declare.Document)
		d.Version = 1
		d.Config.Spread.MaxHops = 9
		return nil
	})
	if err != nil || !called || doc.Config.Spread.MaxHops != 9 || doc.Config.Spread.DecayFactor != 0.8 {
		t.Errorf("custom decoder: called=%v doc=%+v err=%v (it should start from the defaults)", called, doc.Config, err)
	}
	boom := errors.New("boom")
	if _, err := declare.Load(strings.NewReader(""), func(io.Reader, any) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("decoder errors must be wrapped: %v", err)
	}
	// A decoder that leaves the version unset is rejected like any other.
	if _, err := declare.Load(strings.NewReader(""), func(io.Reader, any) error { return nil }); !errors.Is(err, declare.ErrUnsupportedVersion) {
		t.Errorf("err = %v", err)
	}
}

func TestEmbeddedDocumentsLoadAndValidate(t *testing.T) {
	entries, err := testdata.ReadDir("testdata")
	if err != nil || len(entries) < 4 {
		t.Fatalf("testdata: %v (%d files)", err, len(entries))
	}
	for _, e := range entries {
		data, err := testdata.ReadFile("testdata/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		doc, err := declare.Load(bytes.NewReader(data), nil)
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		if err := doc.Config.Validate(); err != nil {
			t.Errorf("%s: %v", e.Name(), err)
		}
	}
}

func TestSpecLoadsEveryNodeShape(t *testing.T) {
	// The grammar in doc/specs/config.md: every construct round-trips through JSON.
	doc, err := loadString(`{"version":1,"rules":[{"id":"r","layer":"u","scope":"s","event":"e","priority":3,"stop":"all",
		"when":{"use":"any","of":[{"use":"not","of":[{"use":"even"}]},{"use":"even"}]},
		"do":[{"use":"if","cond":{"use":"even"},"then":{"use":"seq","of":[{"use":"say","args":{"msg":"x"}}]},"else":{"use":"say","args":{"msg":"y"}}}]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	r := doc.Rules[0]
	if r.ID != "r" || r.Scope != "s" || *r.Priority != 3 || r.Stop != rules.StopAll || r.When.Use != "any" || r.Do[0].Else == nil || r.Do[0].Then.Of[0].Args["msg"] != "x" {
		t.Errorf("rule = %+v", r)
	}
	if _, err := testCatalog(t).Action(r.Do[0]); err != nil {
		t.Error(err)
	}
}
