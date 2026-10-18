package declare_test

import (
	"fmt"
	"strings"

	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/rules"
	"github.com/Whealinghold/rumour_mill/spread"
)

func fired(res rules.Result, err error) string {
	var parts []string
	for _, f := range res.Fired {
		parts = append(parts, f.Layer+"/"+f.RuleID)
	}
	return fmt.Sprintf("[%s] err=%v", strings.Join(parts, " "), err)
}

func show(b *being, now knowledge.Tick) {
	for _, f := range b.kb.Find(knowledge.Query{}, now) {
		fmt.Printf("  %s: %s %s@%.3f %s\n", b.id, f.Type, f.Subject, f.Confidence, f.Source)
	}
}

// A spaceport hub, entirely from testdata/spaceport.json: rules, a spreading
// filter, inference and config (custom source kinds, a default priority).
func Example_spaceport() {
	w := load("spaceport.json", rules.Layer[scene]{Name: "station"}, attrLayer("guild", "guild"))
	vo := w.being("inspector-vo", "guild", "customs", "place", "bay-4")
	ilse := w.being("dockhand-ilse", "guild", "haulers", "place", "bay-2")

	for _, b := range []*being{vo, ilse} {
		res, err := w.reg.Process(scene{self: b, subject: "freighter-aurelia", evtID: "ev-1", place: "bay-4", now: 100}, "cargo_inspected")
		fmt.Println(b.id, fired(res, err))
	}
	// The customs-to-haulers filter blocks the flag but not the seizure.
	w.sp.Spread(vo, []spread.Holder{ilse}, 101)
	show(ilse, 101)
	res, _ := w.eng.Run(vo.kb, 102)
	for _, d := range res.Derived {
		f, _ := vo.kb.Get(d.ID)
		fmt.Printf("derived: %s %s@%.1f %s\n", f.Type, f.Subject, f.Confidence, f.Source)
	}

	// Output:
	// inspector-vo [station/witness-inspection guild/customs-record] err=<nil>
	// dockhand-ilse [] err=<nil>
	//   dockhand-ilse: cargo_seized freighter-aurelia@0.720 hearsay
	// derived: captain_is_smuggler freighter-aurelia@0.7 deduced
}

// An office, from testdata/office.json: a finance rule that stops everything,
// a filter keeping layoff gossip from managers, and a two-pass inference chain.
func Example_office() {
	w := load("office.json", rules.Layer[scene]{Name: "company"}, attrLayer("department", "dept"))
	dana := w.being("dana", "dept", "accounts", "role", "clerk", "place", "boardroom")
	priya := w.being("priya", "dept", "finance", "role", "analyst", "place", "boardroom")
	mo := w.being("mo", "dept", "sales", "role", "manager", "place", "lobby")
	bo := w.being("bo", "dept", "sales", "role", "clerk", "place", "lobby")

	for _, b := range []*being{dana, priya, mo} {
		res, err := w.reg.Process(scene{self: b, subject: "company", place: "boardroom", now: 10}, "meeting_held")
		fmt.Printf("%s %s stopped=%q\n", b.id, fired(res, err), res.Stopped)
	}
	show(priya, 10)

	res, _ := w.sp.Spread(dana, []spread.Holder{mo, bo}, 11)
	fmt.Println("dana told", len(res.Transfers), "of 2 listeners") // the manager is filtered out
	run, _ := w.eng.Run(bo.kb, 12)
	fmt.Println("bo derived", len(run.Derived), "facts in", run.Passes, "passes (max_passes 2)")
	show(bo, 12)

	// Output:
	// dana [company/overhear-closed-door-meeting] err=<nil> stopped=""
	// priya [company/overhear-closed-door-meeting department/finance-knows-the-numbers] err=<nil> stopped="all"
	// mo [] err=<nil> stopped=""
	//   priya: layoffs_planned company@0.950 announcement
	// dana told 1 of 2 listeners
	// bo derived 2 facts in 2 passes (max_passes 2)
	//   bo: morale_dropping company@0.500 guess
	//   bo: resignations_coming company@0.400 guess
	//   bo: layoffs_planned company@0.405 grapevine
}

// A noir sim, from testdata/noir.json: an if/else node in data, a private
// fact that never leaves its holder, and a conclusion drawn only from
// testimony.
func Example_noir() {
	w := load("noir.json", rules.Layer[scene]{Name: "city"}, attrLayer("precinct", "precinct"))
	mabel := w.being("mabel", "precinct", "14th", "nerve", "low", "place", "office-3")
	ned := w.being("ned", "precinct", "14th", "nerve", "high", "place", "office-3")
	cabbie := w.being("cabbie", "precinct", "9th", "place", "street")

	for _, b := range []*being{mabel, ned} {
		w.reg.Process(scene{self: b, subject: "mr-vane", place: "office-3", now: 1}, "interview")
		res, _ := w.eng.Run(b.kb, 2)
		for _, d := range res.Derived {
			f, _ := b.kb.Get(d.ID)
			fmt.Printf("%s: %s %s@%.1f private=%v\n", b.id, f.Type, f.Subject, f.Confidence, f.Private)
		}
	}
	show(mabel, 3)

	res, _ := w.sp.Spread(mabel, []spread.Holder{cabbie}, 3)
	fmt.Println("mabel told the cabbie", len(res.Transfers), "facts; her private ones stayed with her")
	show(cabbie, 3)

	// Output:
	// mabel: person_of_interest mr-vane@0.8 private=true
	// ned: person_of_interest mr-vane@0.8 private=true
	//   mabel: alibi_false mr-vane@0.800 testimony
	//   mabel: case_leaked mr-vane@1.000 testimony
	//   mabel: person_of_interest mr-vane@0.800 hunch
	//   mabel: motive_known mr-vane@0.600 tip
	// mabel told the cabbie 2 facts; her private ones stayed with her
	//   cabbie: alibi_false mr-vane@0.400 rumour
	//   cabbie: motive_known mr-vane@0.300 rumour
}
