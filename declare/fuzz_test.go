package declare_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Whealinghold/rumour_mill/declare"
	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/rules"
	"github.com/Whealinghold/rumour_mill/spread"
)

// FuzzLoad feeds arbitrary bytes through the whole declarative path. Whatever
// the input, the loader must return a value or an error and never panic, and a
// document it accepts must be safe to wire.
func FuzzLoad(f *testing.F) {
	entries, _ := testdata.ReadDir("testdata")
	for _, e := range entries {
		data, _ := testdata.ReadFile("testdata/" + e.Name())
		f.Add(data)
	}
	for _, s := range []string{
		``, `{}`, `[]`, `null`, `{"version":1}`, `{"version":1,"rules":[{}]}`, `{"version":1,"rules":null}`,
		`{"version":1,"rules":[{"id":"a","layer":"u","event":"e","when":{"use":"all","of":[{"use":"all","of":[]}]}}]}`,
		`{"version":1,"config":{"knowledge":{"source_ranks":null}}}`,
		`{"version":1,"config":{"knowledge":{"source_ranks":{"":1}}}}`,
		`{"version":1,"inference":[{"id":"a","requires":[{"type":"t"}],"produces":{"type":"p"},"confidence":0.5}]}`,
		`{"version":1,"spreading":{"filters":[{"use":"not"}]}}`,
		`{"version":1,"rules":[{"id":"a","layer":"u","event":"e","do":[{"use":"if","cond":{"use":"even"}}]}]}`,
		"{\"version\":1,\"rules\":[{\"id\":\"\x1f\",\"layer\":\"u\",\"event\":\"e\"}]}",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := declare.Load(bytes.NewReader(data), nil)
		if err != nil {
			return
		}
		_ = doc.Config.Validate()
		cat := testCatalog(t)
		reg, err := rules.NewRegistry(rules.DefaultConfig(), rules.Layer[cx]{Name: "u"}, rules.Layer[cx]{Name: "k", Scopes: func(cx) []string { return []string{"s"} }})
		if err != nil {
			t.Fatal(err)
		}
		if declare.LoadRules(reg, doc.Config.Rules, doc.Rules, cat) == nil {
			reg.Process(newCx(1), "e")
		}
		if _, err := encounterCatalog().ConditionAll(doc.Spreading.Filters); err == nil {
			_, _ = spread.New(doc.Config.Spread)
		}
		if e, err := infer.New(doc.Config.Infer, doc.Inference); err == nil {
			_, _ = e.Run(nil, 0)
		}
	})
}

// FuzzNode resolves arbitrary JSON as a condition and as an action.
func FuzzNode(f *testing.F) {
	for _, s := range []string{
		`{"use":"even"}`, `{"use":"all","of":[{"use":"even"}]}`, `{"use":"not","of":[]}`, `{"use":"seq","of":[{"use":"say","args":{"msg":1}}]}`,
		`{"use":"if","cond":{"use":"even"},"then":{"use":"fail"},"else":{"use":"say","args":{"msg":"x"}}}`,
		`{"use":"gt","args":{"value":1e400}}`, `{"use":"gt","args":{"value":9223372036854775808}}`, `{"use":"gt","args":{"value":-0.0}}`,
		`{"use":"say","args":{"msg":null}}`, `{"use":"say","args":null}`, `{"args":{}}`, `{"of":[{"of":[{"of":[]}]}]}`,
		`{"use":"attr_equals","args":{"side":"self","key":"n","value":1}}`, `{"use":"attr_equals","args":{"side":"self","key":"n","value":"x"}}`,
		`{"use":"attr_equals","args":{"side":"self","key":"n","value":true}}`, `{"use":"attr_equals","args":{"side":"self","key":"n","value":null}}`,
		`{"use":"attr_equals","args":{"side":"self","key":"n","value":[1]}}`, `{"use":"attr_equals","args":{"side":"self","key":"n","value":{"a":1}}}`,
		`{"use":"attr_in","args":{"side":"self","key":"n","values":[]}}`, `{"use":"attr_in","args":{"side":"peer","key":"n","values":[1,"a",null,true,[2]]}}`,
		`{"use":"attr_same","args":{"side":"self","key":"n"}}`, `{"use":"attr_same","args":{"side":"self","other_side":"peer","key":"n","other_key":"m"}}`,
		`{"use":"add_fact","args":{"type":"t","confidence":0.5,"source":"x","discriminator_from":"parity"}}`,
		`{"use":"add_fact","args":{"type":"t","confidence":0.5,"source":"x","discriminator":"d","discriminator_from":"parity"}}`,
		`{"use":"add_fact","args":{"type":"t","confidence":0.5,"source":"x","discriminator_from":"nope"}}`,
	} {
		f.Add([]byte(s))
	}
	cat := testCatalog(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		var n declare.Node
		if json.Unmarshal(data, &n) != nil {
			return
		}
		if c, err := cat.Condition(n); err == nil && c != nil {
			c(newCx(2))
		}
		if a, err := cat.Action(n); err == nil && a != nil {
			_ = a(newCx(2))
		}
	})
}
