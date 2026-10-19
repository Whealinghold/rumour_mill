package rumourotel_test

import (
	"fmt"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/rules"
	"github.com/Whealinghold/rumour_mill/spread"

	rumourotel "github.com/Whealinghold/rumour_mill/contrib/otel"
)

// Example wires all four adapters and reads the result back from an in-memory reader.
func Example() {
	reader := sdkmetric.NewManualReader()
	m, err := rumourotel.New(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("rumour_mill"),
		rumourotel.WithSourceKinds("observed"))
	if err != nil {
		panic(err)
	}

	ana, bo := newPerson(nil, "ana", m.Store()), newPerson(nil, "bo", m.Store()) // Store(): adds and fades
	ana.kb.Add(knowledge.Fact{ID: "x", Type: "notice", Subject: "s", Confidence: 0.9, Source: "observed"})

	sp, _ := spread.New(spread.DefaultConfig(), m.Spread()) // Spread(): offers, deliveries, drops
	if _, err := sp.Spread(ana, []spread.Holder{bo}, 1); err != nil {
		panic(err)
	}

	en, _ := infer.New(infer.DefaultConfig(), []infer.Rule{{
		ID: "r", Requires: []infer.Requirement{{Type: "notice"}}, Produces: infer.Product{Type: "rumour"}, Confidence: 0.5,
	}}, m.Infer()) // Infer(): runs, passes, derived
	if _, err := en.Run(bo.kb, 2); err != nil {
		panic(err)
	}

	reg, _ := rules.NewRegistry(rules.DefaultConfig(), rules.Layer[int]{Name: "world"})
	m.ObserveRules(reg) // seeds layers; counts processed, fired, stopped
	reg.Register("world", "", rules.Rule[int]{ID: "r", Event: "ping"})
	reg.Process(0, "ping")

	got := snapshot(nil, reader)
	for _, k := range []string{
		"rumour.spread.offered", "rumour.spread.deliveries{outcome=inserted}", "rumour.fact.adds{outcome=inserted,source=observed}",
		"rumour.fact.adds{outcome=inserted,source=other}",
		"rumour.infer.derived{outcome=inserted}", "rumour.rules.fired{layer=world}",
	} {
		fmt.Println(k, got[k])
	}
	// Output:
	// rumour.spread.offered 1
	// rumour.spread.deliveries{outcome=inserted} 1
	// rumour.fact.adds{outcome=inserted,source=observed} 1
	// rumour.fact.adds{outcome=inserted,source=other} 2
	// rumour.infer.derived{outcome=inserted} 1
	// rumour.rules.fired{layer=world} 1
}
