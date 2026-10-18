package rules_test

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Whealinghold/rumour_mill/logic"
	"github.com/Whealinghold/rumour_mill/rules"
)

// A spaceport hub: three layers (station-wide, per guild, per individual).
// Customs staff have a high-priority guild rule that ends the guild layer, so
// their lower-priority guild chatter never runs; the individual layer still does.
func Example_spaceport() {
	type crew struct {
		id, dock string
		guilds   []string
		notes    []string
	}
	type portCtx struct {
		self      *crew
		eventDock string
	}
	note := func(s string) logic.Action[portCtx] {
		return func(c portCtx) error { c.self.notes = append(c.self.notes, s); return nil }
	}

	reg, err := rules.NewRegistry(rules.DefaultConfig(),
		rules.Layer[portCtx]{Name: "station"}, // nil Scopes: everyone
		rules.Layer[portCtx]{Name: "guild", Scopes: func(c portCtx) []string { return c.self.guilds }},
		rules.Layer[portCtx]{Name: "individual", Scopes: func(c portCtx) []string { return []string{c.self.id} }},
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	sameDock := func(c portCtx) bool { return c.self.dock == c.eventDock }
	for _, r := range []struct {
		layer, scope string
		rule         rules.Rule[portCtx]
	}{
		{"station", "", rules.Rule[portCtx]{ID: "witness", Event: "cargo_inspected", When: sameDock, Do: note("saw the inspection"), Priority: 10}},
		{"guild", "customs", rules.Rule[portCtx]{ID: "official-record", Event: "cargo_inspected", Do: note("filed the record"), Priority: 5, Stop: rules.StopLayer}},
		{"guild", "customs", rules.Rule[portCtx]{ID: "idle-chatter", Event: "cargo_inspected", Do: note("chatted"), Priority: 20}},
		{"guild", "haulers", rules.Rule[portCtx]{ID: "grumble", Event: "cargo_inspected", Do: note("grumbled about delays"), Priority: 20}},
		{"individual", "broker-oyelaran", rules.Rule[portCtx]{ID: "re-plan", Event: "cargo_inspected", Do: note("revised the schedule")}},
	} {
		if err := reg.Register(r.layer, r.scope, r.rule); err != nil {
			fmt.Println(err)
			return
		}
	}

	for _, c := range []*crew{
		{id: "inspector-vo", dock: "bay-4", guilds: []string{"customs"}},
		{id: "dockhand-ilse", dock: "bay-4", guilds: []string{"haulers"}},
		{id: "broker-oyelaran", dock: "concourse", guilds: []string{"haulers", "brokers"}},
	} {
		res, _ := reg.Process(portCtx{self: c, eventDock: "bay-4"}, "cargo_inspected")
		var fired []string
		for _, f := range res.Fired {
			fired = append(fired, f.Layer+"/"+f.RuleID)
		}
		fmt.Printf("%s: %s | %s\n", c.id, strings.Join(fired, " "), strings.Join(c.notes, "; "))
	}

	// Output:
	// inspector-vo: station/witness guild/official-record | saw the inspection; filed the record
	// dockhand-ilse: station/witness guild/grumble | saw the inspection; grumbled about delays
	// broker-oyelaran: guild/grumble individual/re-plan | grumbled about delays; revised the schedule
}

// An office: finance staff are told the real numbers. Their rule stops
// everything after it, including the later "person" layer, which an ordinary
// employee still reaches.
func Example_office() {
	type employee struct {
		id, dept string
		heard    []string
	}
	type officeCtx struct{ who *employee }
	hear := func(s string) logic.Action[officeCtx] {
		return func(c officeCtx) error { c.who.heard = append(c.who.heard, s); return nil }
	}

	reg, _ := rules.NewRegistry(rules.DefaultConfig(),
		rules.Layer[officeCtx]{Name: "company"},
		rules.Layer[officeCtx]{Name: "department", Scopes: func(c officeCtx) []string { return []string{c.who.dept} }},
		rules.Layer[officeCtx]{Name: "person", Scopes: func(c officeCtx) []string { return []string{c.who.id} }},
	)
	reg.Register("company", "", rules.Rule[officeCtx]{ID: "overhear", Event: "meeting_held", Do: hear("overheard a closed-door meeting")})
	reg.Register("department", "finance", rules.Rule[officeCtx]{ID: "know-the-numbers", Event: "meeting_held", Priority: 1,
		Stop: rules.StopAll, Do: hear("was shown the real numbers")})
	reg.Register("person", "dana", rules.Rule[officeCtx]{ID: "assume-the-worst", Event: "meeting_held", Do: hear("assumed the worst")})
	reg.Register("person", "priya", rules.Rule[officeCtx]{ID: "assume-the-worst", Event: "meeting_held", Do: hear("assumed the worst")})

	for _, e := range []*employee{{id: "dana", dept: "accounts"}, {id: "priya", dept: "finance"}} {
		res, _ := reg.Process(officeCtx{e}, "meeting_held")
		fmt.Printf("%s: %q stopped=%q\n", e.id, e.heard, res.Stopped)
	}

	// Output:
	// dana: ["overheard a closed-door meeting" "assumed the worst"] stopped=""
	// priya: ["overheard a closed-door meeting" "was shown the real numbers"] stopped="all"
}

// A noir detective sim: equal priorities run in registration order, a failing
// action is reported but does not stop the pipeline (the default), and rules
// can be removed again.
func Example_noir() {
	type witness struct{ name, precinct string }
	var log []string
	step := func(s string) logic.Action[witness] {
		return func(witness) error { log = append(log, s); return nil }
	}

	reg, _ := rules.NewRegistry(rules.DefaultConfig(),
		rules.Layer[witness]{Name: "city"},
		rules.Layer[witness]{Name: "precinct", Scopes: func(w witness) []string { return []string{w.precinct} }},
	)
	reg.Register("city", "", rules.Rule[witness]{ID: "log-statement", Event: "interview", Do: step("statement logged")})
	reg.Register("city", "", rules.Rule[witness]{ID: "cross-check", Event: "interview", Do: step("cross-checked")}) // same priority: runs second
	reg.Register("precinct", "14th", rules.Rule[witness]{ID: "lean-on-witness", Event: "interview",
		Do: func(witness) error { return errors.New("notebook is wet") }})
	reg.Register("precinct", "14th", rules.Rule[witness]{ID: "follow-up", Event: "interview", Priority: 50, Do: step("follow-up booked")})

	w := witness{name: "mabel", precinct: "14th"}
	res, err := reg.Process(w, "interview")
	fmt.Println(len(res.Fired), "fired;", log)
	fmt.Println("error:", err)

	reg.Unregister("precinct", "14th", "lean-on-witness")
	log = nil
	res, err = reg.Process(w, "interview")
	fmt.Println(len(res.Fired), "fired;", log, "error:", err)

	// Output:
	// 4 fired; [statement logged cross-checked follow-up booked]
	// error: rules: precinct/14th/lean-on-witness: notebook is wet
	// 3 fired; [statement logged cross-checked follow-up booked] error: <nil>
}
