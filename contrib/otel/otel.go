// Package rumourotel exports rumour_mill's metrics through an OpenTelemetry
// Meter. It is a separate module so the core library keeps no dependencies, and
// it depends on the OpenTelemetry metric API only: the application owns the SDK
// and the exporter (for example Prometheus or OTLP). It never reads the global
// MeterProvider.
//
// Wire the adapters in once; each one observes the matching component:
//
//	m, err := rumourotel.New(meterProvider.Meter("rumour_mill"),
//		rumourotel.WithSourceKinds("seen", "heard"))
//	st, _ := knowledge.NewStore(kcfg, m.Store())
//	sp, _ := spread.New(scfg, m.Spread())
//	en, _ := infer.New(icfg, irules, m.Infer())
//	m.ObserveRules(reg) // reg is a *rules.Registry; seeds per-layer series
//
// The adapter never injects a clock. Durations are recorded only for
// components you give a clock (knowledge.WithClock, spread.WithClock,
// infer.WithClock, rules.Registry.SetClock).
//
// Store() must be passed to every store you want counted. Spread, Infer and
// Rules each count only the component they are given to.
//
// Instruments (names are dotted; the Prometheus exporter turns them into
// rumour_spread_dropped_total and so on). Counters:
//
//	rumour.spread.calls, rumour.spread.skipped{reason},
//	rumour.spread.listeners.skipped{reason}, rumour.spread.offered,
//	rumour.spread.deliveries{outcome}, rumour.spread.dropped{reason},
//	rumour.spread.withheld{reason}, rumour.spread.errors, rumour.holder.type_mismatch,
//	rumour.fact.adds{outcome,source}, rumour.fact.add.errors{reason},
//	rumour.fact.evictions, rumour.fact.pruned, rumour.prune.runs,
//	rumour.fade.runs{fader}, rumour.fade.examined, rumour.fade.changed,
//	rumour.fade.forgotten, rumour.fade.invalid,
//	rumour.infer.runs, rumour.infer.passes, rumour.infer.truncated,
//	rumour.infer.derived{outcome}, rumour.infer.errors,
//	rumour.rules.processed, rumour.rules.unmatched, rumour.rules.considered{layer},
//	rumour.rules.skipped{layer}, rumour.rules.fired{layer},
//	rumour.rules.action.errors{layer}, rumour.rules.stopped{mode},
//	rumour.rules.errors, rumour.rules.register{result}, rumour.rules.unregister{result}
//
// Gauges: rumour.store.size, rumour.store.size.max, rumour.rules.registered{layer}.
// Histograms: rumour.spread.delivered.hops, rumour.spread.delivered.confidence,
// rumour.operation.duration{op} (seconds).
//
// New records a zero for every counter series whose label values form a closed
// set, so each is exported before its first event and rate() and absent()
// alerts work. Layer series are seeded by ObserveRules. OpenTelemetry
// histograms are not seeded: one appears after its first observation. The
// source label is bounded: it is a kind given to WithSourceKinds or "other".
// Attributes are never fact IDs, subjects, rule IDs or event types.
//
// rumour.fact.adds counts adds that returned no error (a rejected add is still
// an add); failed adds are in rumour.fact.add.errors. rumour.spread.withheld
// counts facts the teller never offered; they are not in offered or dropped.
// All Metrics methods are safe for concurrent use.
package rumourotel

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/rules"
	"github.com/Whealinghold/rumour_mill/spread"
)

const other = "other"

// DefaultDurationBuckets returns the default duration histogram boundaries, in
// seconds. Each call returns a fresh slice; safe for concurrent use.
func DefaultDurationBuckets() []float64 {
	return []float64{1e-6, 5e-6, 2.5e-5, 1e-4, 5e-4, 2.5e-3, 1e-2, 5e-2, 0.25, 1}
}

// DefaultHopBuckets returns the default delivered-hops histogram boundaries.
// Each call returns a fresh slice; safe for concurrent use.
func DefaultHopBuckets() []float64 { return []float64{1, 2, 3, 4, 5, 6, 8, 10, 15, 20} }

// DefaultConfidenceBuckets returns the default delivered-confidence histogram
// boundaries, 0.1 to 1.0 in steps of 0.1. Each call returns a fresh slice; safe
// for concurrent use.
func DefaultConfidenceBuckets() []float64 {
	return []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1}
}

type options struct {
	kinds                      []string
	duration, hops, confidence []float64
}

// Option configures New. Options are applied once, at New.
type Option func(*options)

// WithSourceKinds sets the known fact source kinds for the source label of
// rumour.fact.adds; any other source is reported as "other". The argument is
// copied. An empty kind makes New fail.
func WithSourceKinds(kinds ...string) Option {
	return func(o *options) { o.kinds = append([]string(nil), kinds...) }
}

// WithDurationBuckets overrides the duration histogram boundaries (seconds).
func WithDurationBuckets(seconds ...float64) Option {
	return func(o *options) { o.duration = append([]float64(nil), seconds...) }
}

// WithHopBuckets overrides the delivered-hops histogram boundaries.
func WithHopBuckets(hops ...float64) Option {
	return func(o *options) { o.hops = append([]float64(nil), hops...) }
}

// WithConfidenceBuckets overrides the delivered-confidence histogram boundaries.
func WithConfidenceBuckets(c ...float64) Option {
	return func(o *options) { o.confidence = append([]float64(nil), c...) }
}

func checkBuckets(name string, b []float64) error {
	if len(b) == 0 {
		return fmt.Errorf("rumourotel: %s buckets empty", name)
	}
	for i, v := range b {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("rumourotel: %s buckets contain NaN or Inf", name)
		}
		if i > 0 && v <= b[i-1] {
			return fmt.Errorf("rumourotel: %s buckets not strictly increasing", name)
		}
	}
	return nil
}

// RuleRegistry is satisfied by every *rules.Registry[C]. It is what
// ObserveRules needs to seed and observe a registry.
type RuleRegistry interface {
	Layers() []string
	RuleCount(layer string) int
	Observe(rules.Hooks)
}

// Metrics holds the instruments. Instruments are safe for concurrent use, so
// one Metrics may serve many stores, spreaders, engines and registries.
type Metrics struct {
	calls, skipped, listenersSkipped, offered, deliveries, dropped, withheld metric.Int64Counter
	spreadErrors, mismatches                                                 metric.Int64Counter
	adds, addErrors, evictions, pruned, pruneRuns                            metric.Int64Counter
	fadeRuns, fadeExamined, fadeChanged, fadeForgotten, fadeInvalid          metric.Int64Counter
	inferRuns, inferPasses, inferTruncated, inferDerived, inferErrors        metric.Int64Counter
	rulesProcessed, rulesUnmatched, rulesConsidered, rulesSkipped            metric.Int64Counter
	rulesFired, rulesActionErrors, rulesStopped, rulesErrors                 metric.Int64Counter
	rulesRegister, rulesUnregister                                           metric.Int64Counter

	storeSize, storeSizeMax, rulesRegistered metric.Int64Gauge
	hops, confidence, duration               metric.Float64Histogram

	kinds    map[string]bool
	kindList []string

	mu      sync.Mutex // guards sizeMax
	sizeMax int
}

// New creates the instruments on meter and zero-seeds the closed-set series. It
// returns the first option or instrument error.
func New(meter metric.Meter, opts ...Option) (*Metrics, error) {
	o := options{duration: DefaultDurationBuckets(), hops: DefaultHopBuckets(), confidence: DefaultConfidenceBuckets()}
	for _, f := range opts {
		if f != nil {
			f(&o)
		}
	}
	for _, b := range []struct {
		n string
		v []float64
	}{{"duration", o.duration}, {"hop", o.hops}, {"confidence", o.confidence}} {
		if err := checkBuckets(b.n, b.v); err != nil {
			return nil, err
		}
	}
	m := &Metrics{kinds: map[string]bool{}}
	for _, k := range o.kinds {
		if k == "" {
			return nil, errors.New("rumourotel: empty source kind")
		}
		if !m.kinds[k] {
			m.kinds[k] = true
			m.kindList = append(m.kindList, k)
		}
	}
	for _, c := range []struct {
		dst        *metric.Int64Counter
		name, desc string
	}{
		{&m.calls, "rumour.spread.calls", "Spread calls that ran (a teller with a store)."},
		{&m.skipped, "rumour.spread.skipped", "Spread calls that returned early, by reason (nil_teller, no_store)."},
		{&m.listenersSkipped, "rumour.spread.listeners.skipped", "Listeners skipped by Spread, by reason (nil_listener, no_store)."},
		{&m.offered, "rumour.spread.offered", "Fact and listener pairs considered by Spread."},
		{&m.deliveries, "rumour.spread.deliveries", "Copies handed to a listener's store, by what the store did."},
		{&m.dropped, "rumour.spread.dropped", "Offers that did not become a delivery, by reason."},
		{&m.withheld, "rumour.spread.withheld", "Facts the teller held that were never offered, by reason (private, max_hops, below_source_confidence, expired)."},
		{&m.spreadErrors, "rumour.spread.errors", "Spread calls that returned an error."},
		{&m.mismatches, "rumour.holder.type_mismatch", "Typed filters or transforms given a holder of the wrong type (a wiring bug)."},
		{&m.adds, "rumour.fact.adds", "Store adds that returned no error, by outcome and source kind (a rejected add is still an add)."},
		{&m.addErrors, "rumour.fact.add.errors", "Store adds that returned an error, by reason (invalid, identity_mismatch)."},
		{&m.evictions, "rumour.fact.evictions", "Facts evicted to honour the store's MaxFacts."},
		{&m.pruned, "rumour.fact.pruned", "Facts removed by Prune."},
		{&m.pruneRuns, "rumour.prune.runs", "Prune calls."},
		{&m.fadeRuns, "rumour.fade.runs", "Fade calls, by whether a Fader is wired (wired, none)."},
		{&m.fadeExamined, "rumour.fade.examined", "Facts the Fader was called on."},
		{&m.fadeChanged, "rumour.fade.changed", "Facts that survived Fade with a different confidence."},
		{&m.fadeForgotten, "rumour.fade.forgotten", "Facts removed by Fade."},
		{&m.fadeInvalid, "rumour.fade.invalid", "Facts whose Fader result was NaN (a buggy Fader)."},
		{&m.inferRuns, "rumour.infer.runs", "Inference runs."},
		{&m.inferPasses, "rumour.infer.passes", "Inference passes executed."},
		{&m.inferTruncated, "rumour.infer.truncated", "Inference runs that hit MaxPasses while the last pass still changed something."},
		{&m.inferDerived, "rumour.infer.derived", "Facts derived by inference, by store outcome."},
		{&m.inferErrors, "rumour.infer.errors", "Inference runs that returned an error."},
		{&m.rulesProcessed, "rumour.rules.processed", "Events processed by a rules registry."},
		{&m.rulesUnmatched, "rumour.rules.unmatched", "Process calls where no layer had a candidate rule for the event type."},
		{&m.rulesConsidered, "rumour.rules.considered", "Candidate rules considered, by layer."},
		{&m.rulesSkipped, "rumour.rules.skipped", "Candidate rules whose When was false, by layer."},
		{&m.rulesFired, "rumour.rules.fired", "Rules fired, by layer."},
		{&m.rulesActionErrors, "rumour.rules.action.errors", "Fired rules whose Do returned an error, by layer."},
		{&m.rulesStopped, "rumour.rules.stopped", "Process calls ended early, by stop mode."},
		{&m.rulesErrors, "rumour.rules.errors", "Process calls that returned an error."},
		{&m.rulesRegister, "rumour.rules.register", "Register calls, by result (ok, duplicate, invalid, unknown_layer)."},
		{&m.rulesUnregister, "rumour.rules.unregister", "Unregister calls, by result (removed, not_found)."},
	} {
		ctr, err := meter.Int64Counter(c.name, metric.WithDescription(c.desc))
		if err != nil {
			return nil, err
		}
		*c.dst = ctr
	}
	for _, g := range []struct {
		dst        *metric.Int64Gauge
		name, desc string
		unit       string
	}{
		{&m.storeSize, "rumour.store.size", "Fact count of the store last touched.", "{fact}"},
		{&m.storeSizeMax, "rumour.store.size.max", "High-water mark of any observed store size.", "{fact}"},
		{&m.rulesRegistered, "rumour.rules.registered", "Rules currently registered, by layer.", "{rule}"},
	} {
		gg, err := meter.Int64Gauge(g.name, metric.WithDescription(g.desc), metric.WithUnit(g.unit))
		if err != nil {
			return nil, err
		}
		*g.dst = gg
	}
	for _, h := range []struct {
		dst        *metric.Float64Histogram
		name, desc string
		unit       string
		buckets    []float64
	}{
		{&m.hops, "rumour.spread.delivered.hops", "Hops of copies that landed in a store.", "{hop}", o.hops},
		{&m.confidence, "rumour.spread.delivered.confidence", "Confidence of copies that landed in a store.", "1", o.confidence},
		{&m.duration, "rumour.operation.duration", "Wall time per operation; recorded only when a clock is injected.", "s", o.duration},
	} {
		hh, err := meter.Float64Histogram(h.name, metric.WithDescription(h.desc), metric.WithUnit(h.unit),
			metric.WithExplicitBucketBoundaries(h.buckets...))
		if err != nil {
			return nil, err
		}
		*h.dst = hh
	}
	m.seed()
	return m, nil
}

func attr(key, val string) metric.AddOption {
	return metric.WithAttributes(attribute.String(key, val))
}

func (m *Metrics) source(s string) string {
	if m.kinds[s] {
		return s
	}
	return other
}

// recordSize sets the size gauge and raises the high-water mark.
func (m *Metrics) recordSize(ctx context.Context, n int) {
	m.storeSize.Record(ctx, int64(n))
	m.mu.Lock()
	defer m.mu.Unlock()
	if n > m.sizeMax {
		m.sizeMax = n
		m.storeSizeMax.Record(ctx, int64(n))
	}
}

// Spread returns the option that records every Spread call. It chains with
// other spread.WithHooks options.
func (m *Metrics) Spread() spread.Option {
	ctx := context.Background() // the hooks carry no context
	return spread.WithHooks(spread.Hooks{
		OnSpread: func(r spread.Result, err error) {
			m.calls.Add(ctx, 1)
			m.offered.Add(ctx, int64(r.Offered))
			for _, t := range r.Transfers {
				m.deliveries.Add(ctx, 1, attr("outcome", t.Outcome.String()))
				if t.Outcome != knowledge.Rejected {
					m.hops.Record(ctx, float64(t.Hops))
					m.confidence.Record(ctx, t.Confidence)
				}
			}
			r.Dropped.Each(func(reason string, n int) { m.dropped.Add(ctx, int64(n), attr("reason", reason)) })
			r.Withheld.Each(func(reason string, n int) { m.withheld.Add(ctx, int64(n), attr("reason", reason)) })
			r.SkippedListeners.Each(func(reason string, n int) { m.listenersSkipped.Add(ctx, int64(n), attr("reason", reason)) })
			m.mismatches.Add(ctx, int64(r.HolderMismatches))
			if err != nil {
				m.spreadErrors.Add(ctx, 1)
			}
		},
		OnSkip:       func(reason string) { m.skipped.Add(ctx, 1, attr("reason", reason)) },
		OnSpreadDone: func(d time.Duration) { m.observe(ctx, "spread", d) },
	})
}

func (m *Metrics) observe(ctx context.Context, op string, d time.Duration) {
	m.duration.Record(ctx, max(d, 0).Seconds(), metric.WithAttributes(attribute.String("op", op)))
}

// Store returns the option that records adds, add errors, evictions, prunes,
// fades and store size of a store. Pass it to every knowledge.NewStore you want
// counted.
func (m *Metrics) Store() knowledge.Option {
	ctx := context.Background()
	return knowledge.WithHooks(knowledge.Hooks{
		OnAdd: func(f knowledge.Fact, r knowledge.AddResult) {
			m.adds.Add(ctx, 1, metric.WithAttributes(
				attribute.String("outcome", r.Outcome.String()), attribute.String("source", m.source(f.Source))))
			m.evictions.Add(ctx, int64(len(r.Evicted)))
			m.recordSize(ctx, r.Size)
		},
		OnAddError: func(_ knowledge.Fact, reason string, _ error) {
			m.addErrors.Add(ctx, 1, attr("reason", reason))
		},
		OnPrune: func(r knowledge.PruneResult) {
			m.pruneRuns.Add(ctx, 1)
			m.pruned.Add(ctx, int64(r.Removed))
			m.recordSize(ctx, r.Size)
		},
		OnFade: func(r knowledge.FadeResult) {
			fader := "none"
			if r.Wired {
				fader = "wired"
			}
			m.fadeRuns.Add(ctx, 1, attr("fader", fader))
			m.fadeExamined.Add(ctx, int64(r.Examined))
			m.fadeChanged.Add(ctx, int64(len(r.Changed)))
			m.fadeForgotten.Add(ctx, int64(len(r.Forgotten)))
			m.fadeInvalid.Add(ctx, int64(len(r.Invalid)))
			m.recordSize(ctx, r.Size)
		},
		OnDuration: func(op string, d time.Duration) {
			if op == knowledge.OpFind {
				op = "store_find"
			}
			m.observe(ctx, op, d)
		},
	})
}

// Infer returns the option that records every inference Run.
func (m *Metrics) Infer() infer.Option {
	ctx := context.Background()
	return infer.WithHooks(infer.Hooks{
		OnRun: func(r infer.Result, err error) {
			m.inferRuns.Add(ctx, 1)
			m.inferPasses.Add(ctx, int64(r.Passes))
			if r.Truncated {
				m.inferTruncated.Add(ctx, 1)
			}
			for _, d := range r.Derived {
				m.inferDerived.Add(ctx, 1, attr("outcome", d.Outcome.String()))
			}
			if err != nil {
				m.inferErrors.Add(ctx, 1)
			}
		},
		OnRunDone: func(d time.Duration) { m.observe(ctx, "infer_run", d) },
	})
}

// Rules returns the hooks that record every Process, Register and Unregister
// call. Layer series are not pre-seeded; use ObserveRules for that.
func (m *Metrics) Rules() rules.Hooks {
	ctx := context.Background()
	return rules.Hooks{
		OnProcess: func(r rules.Result, err error) {
			m.rulesProcessed.Add(ctx, 1)
			if r.Unmatched() {
				m.rulesUnmatched.Add(ctx, 1)
			}
			for _, l := range r.Layers {
				m.rulesConsidered.Add(ctx, int64(l.Candidates), attr("layer", l.Layer))
				m.rulesSkipped.Add(ctx, int64(l.Skipped), attr("layer", l.Layer))
				m.rulesActionErrors.Add(ctx, int64(l.Failed), attr("layer", l.Layer))
			}
			for _, f := range r.Fired {
				m.rulesFired.Add(ctx, 1, attr("layer", f.Layer))
			}
			if r.Stopped != rules.StopNone {
				m.rulesStopped.Add(ctx, 1, attr("mode", string(r.Stopped)))
			}
			if err != nil {
				m.rulesErrors.Add(ctx, 1)
			}
		},
		OnProcessDone: func(d time.Duration) { m.observe(ctx, "rules_process", d) },
		OnRegister: func(e rules.RegisterEvent) {
			res := "ok"
			switch {
			case e.Err == nil:
			case errors.Is(e.Err, rules.ErrDuplicateRule):
				res = "duplicate"
			case errors.Is(e.Err, rules.ErrUnknownLayer):
				res = "unknown_layer"
			default: // ErrInvalidRule and anything unforeseen
				res = "invalid"
			}
			m.rulesRegister.Add(ctx, 1, attr("result", res))
			if e.Err == nil || e.Count > 0 { // skip unknown layers: the label stays bounded
				m.rulesRegistered.Record(ctx, int64(e.Count), metric.WithAttributes(attribute.String("layer", e.Layer)))
			}
		},
		OnUnregister: func(e rules.UnregisterEvent) {
			res := "not_found"
			if e.Removed {
				res = "removed"
				m.rulesRegistered.Record(ctx, int64(e.Count), metric.WithAttributes(attribute.String("layer", e.Layer)))
			}
			m.rulesUnregister.Add(ctx, 1, attr("result", res))
		},
	}
}

// ObserveRules seeds every per-layer series from r.Layers(), sets
// rumour.rules.registered to the current rule counts, then observes r with
// Rules(). Use it instead of r.Observe(m.Rules()). Call it once per registry at
// wiring time.
func (m *Metrics) ObserveRules(r RuleRegistry) {
	ctx := context.Background()
	for _, l := range r.Layers() {
		for _, c := range []metric.Int64Counter{m.rulesConsidered, m.rulesSkipped, m.rulesFired, m.rulesActionErrors} {
			c.Add(ctx, 0, attr("layer", l))
		}
		m.rulesRegistered.Record(ctx, int64(r.RuleCount(l)), metric.WithAttributes(attribute.String("layer", l)))
	}
	r.Observe(m.Rules())
}

// seed adds zero so every closed-set series exists before the first event.
func (m *Metrics) seed() {
	ctx := context.Background()
	for _, c := range []metric.Int64Counter{
		m.calls, m.offered, m.spreadErrors, m.mismatches, m.evictions, m.pruned, m.pruneRuns,
		m.fadeExamined, m.fadeChanged, m.fadeForgotten, m.fadeInvalid,
		m.inferRuns, m.inferPasses, m.inferTruncated, m.inferErrors,
		m.rulesProcessed, m.rulesUnmatched, m.rulesErrors,
	} {
		c.Add(ctx, 0)
	}
	m.storeSize.Record(ctx, 0)
	m.storeSizeMax.Record(ctx, 0)
	seedEach := func(c metric.Int64Counter, key string, vals ...string) {
		for _, v := range vals {
			c.Add(ctx, 0, attr(key, v))
		}
	}
	each := func(c metric.Int64Counter, e func(func(string, int))) {
		e(func(reason string, _ int) { c.Add(ctx, 0, attr("reason", reason)) })
	}
	each(m.dropped, spread.Dropped{}.Each)
	each(m.withheld, spread.Withheld{}.Each)
	each(m.listenersSkipped, spread.SkippedListeners{}.Each)
	seedEach(m.skipped, "reason", spread.SkipReasons()...)
	seedEach(m.addErrors, "reason", knowledge.AddErrorReasons()...)
	seedEach(m.fadeRuns, "fader", "wired", "none")
	seedEach(m.rulesRegister, "result", "ok", "duplicate", "invalid", "unknown_layer")
	seedEach(m.rulesUnregister, "result", "removed", "not_found")
	seedEach(m.rulesStopped, "mode", string(rules.StopLayer), string(rules.StopAll))
	// A derived fact is inserted or replaced; rejected can never occur, so it is not seeded.
	seedEach(m.inferDerived, "outcome", knowledge.Inserted.String(), knowledge.Replaced.String())
	srcs := append(append([]string(nil), m.kindList...), other)
	if m.kinds[other] {
		srcs = m.kindList
	}
	for _, o := range []knowledge.Outcome{knowledge.Rejected, knowledge.Inserted, knowledge.Replaced} {
		seedEach(m.deliveries, "outcome", o.String())
		for _, s := range srcs {
			m.adds.Add(ctx, 0, metric.WithAttributes(attribute.String("outcome", o.String()), attribute.String("source", s)))
		}
	}
}
