// Package knowledge stores what one holder believes: facts with a confidence,
// a ranked source kind, provenance and an optional expiry.
//
// Fact types, source kinds and their ranks are all caller-defined strings and
// config; the package contains no domain vocabulary. Time is an abstract Tick.
package knowledge

// Tick is the library's only notion of time. What one tick means (a minute, a
// turn, a day) is the caller's decision. Larger is later.
type Tick int64

// Fact is one claim held by one holder. Payload is shared by reference and
// must be treated as immutable; Prov.Premises is copied on the way in and out
// of a Store.
type Fact struct {
	ID            string  // identity; derived from Type, Subject and Discriminator when empty
	Type          string  // caller-defined kind of claim; required
	Subject       string  // who or what it is about; may be empty
	Discriminator string  // optional extra identity part (which victim, which item, which event)
	Payload       any     // optional caller data; never inspected
	Confidence    float64 // belief in [0,1]; clamped on Add
	Source        string  // caller-defined source kind; required
	Prov          Provenance
	Observed      Tick  // when this holder acquired or last upgraded the fact
	FadedAt       Tick  // tick through which Fade has been applied; 0 = never faded; not part of identity, not validated by Add
	Expiry        *Tick // nil = never; expired when *Expiry <= now
	Private       bool  // never leaves the holder through spreading
}

// Provenance records where a fact came from.
type Provenance struct {
	From     string   // holder that told this holder; empty if first-hand
	Hops     int      // tellings between the origin and this holder; 0 = first-hand
	EventID  string   // caller's ID of the causing event, if any
	Premises []string // fact IDs an inference used; empty otherwise
}

// Query selects facts. Zero-valued fields match everything.
type Query struct {
	Type          string   // "" = any type
	Subject       string   // "" = any subject
	Sources       []string // empty = any source kind
	MinConfidence *float64 // nil = Config.QueryFloor
}

// Outcome says what Add did with a fact.
type Outcome int

// The possible outcomes of Add.
const (
	Rejected Outcome = iota // the existing fact (or the cap) won
	Inserted                // a new fact was stored
	Replaced                // an existing fact was overwritten
)

// String returns "rejected", "inserted" or "replaced".
func (o Outcome) String() string {
	switch o {
	case Rejected:
		return "rejected"
	case Inserted:
		return "inserted"
	case Replaced:
		return "replaced"
	}
	return "unknown"
}

// AddResult reports the effect of Add. It is a plain value, safe from any goroutine once returned.
type AddResult struct {
	ID      string
	Outcome Outcome
	Evicted []string // IDs removed to honour Config.MaxFacts, in eviction order
	Size    int      // Len() after cap enforcement; 0 when Add failed
}

// Decision is a MergePolicy's verdict.
type Decision int

// The possible merge decisions.
const (
	Keep    Decision = iota // keep the existing fact
	Replace                 // overwrite with the incoming fact
)

// MergePolicy decides what happens when incoming has the same ID as existing.
// rank maps a source kind to its configured rank.
type MergePolicy func(existing, incoming Fact, rank func(source string) int) Decision

// RankThenConfidence is the default MergePolicy: a higher-ranked source wins,
// at equal rank higher confidence wins, and ties keep the existing fact so
// re-adding the same fact is idempotent.
func RankThenConfidence(existing, incoming Fact, rank func(string) int) Decision {
	re, ri := rank(existing.Source), rank(incoming.Source)
	switch {
	case ri > re:
		return Replace
	case ri < re:
		return Keep
	case incoming.Confidence > existing.Confidence:
		return Replace
	}
	return Keep
}
