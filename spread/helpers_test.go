package spread_test

import (
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/spread"
)

// person is a minimal caller type that satisfies spread.Holder.
type person struct {
	id    string
	kb    *knowledge.Store
	group string
	trust map[string]int
}

func (p *person) ID() string                  { return p.id }
func (p *person) Knowledge() *knowledge.Store { return p.kb }

// newPerson builds a person with a fresh store; it panics on a bad config
// because a test or example with one is simply wrong.
func newPerson(id string, cfg knowledge.Config) *person {
	kb, err := knowledge.NewStore(cfg)
	if err != nil {
		panic(err)
	}
	return &person{id: id, kb: kb}
}

func holders(ps ...*person) []spread.Holder {
	out := make([]spread.Holder, len(ps))
	for i, p := range ps {
		out[i] = p
	}
	return out
}
