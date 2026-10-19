// Package rumourprom exports rumour_mill's metrics to Prometheus. It is a
// separate module so the core library keeps no dependencies.
//
// Wire the observers; each one is optional and independent:
//
//	m, err := rumourprom.New(prometheus.DefaultRegisterer,
//		rumourprom.WithSourceKinds("seen", "heard"))
//	store, _ := knowledge.NewStore(cfg, m.Store())      // adds, errors, fades, prunes, size
//	sp, _ := spread.New(scfg, m.Spread())               // spread calls, drops, hops
//	eng, _ := infer.New(icfg, rs, m.Infer())            // inference runs
//	m.ObserveRules(registry)                            // rule processing, seeds layers
//
// Durations are recorded only when the caller injects a clock into the core
// component (WithClock, Registry.SetClock); the adapter never reads time.
//
// Counters (all names end in _total):
//
//	rumour_spread_calls, _skipped{reason}, _listeners_skipped{reason},
//	rumour_spread_offered, _deliveries{outcome}, _dropped{reason},
//	rumour_spread_withheld{reason}, _errors, rumour_holder_type_mismatch
//	rumour_fact_adds{outcome,source}, rumour_fact_add_errors{reason},
//	rumour_fact_evictions, rumour_fact_pruned, rumour_prune_runs
//	rumour_fade_runs{fader}, _examined, _changed, _forgotten, _invalid
//	rumour_infer_runs, _passes, _truncated, _derived{outcome}, _errors
//	rumour_rules_processed, _unmatched, _errors, _stopped{mode},
//	rumour_rules_considered{layer}, _skipped{layer}, _fired{layer},
//	rumour_rules_action_errors{layer}, _register{result}, _unregister{result}
//
// Gauges: rumour_store_size, rumour_store_size_max, rumour_rules_registered{layer}.
//
// Histograms: rumour_spread_delivered_hops, rumour_spread_delivered_confidence,
// rumour_operation_duration_seconds{op}. Buckets default to
// DefaultHopBuckets, DefaultConfidenceBuckets and DefaultDurationBuckets and
// are overridable with options.
//
// Series with a closed set of label values exist at zero from New. Layer
// series are seeded by ObserveRules. The source label is the caller-supplied
// WithSourceKinds set plus "other", so series count stays bounded; labels are
// never fact IDs, subjects, rule IDs or event types.
package rumourprom

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Whealinghold/rumour_mill/infer"
	"github.com/Whealinghold/rumour_mill/knowledge"
	"github.com/Whealinghold/rumour_mill/rules"
	"github.com/Whealinghold/rumour_mill/spread"
)

// otherSource is the source label for every kind not given to WithSourceKinds.
const otherSource = "other"

// Operation labels of rumour_operation_duration_seconds.
var ops = []string{"spread", "infer_run", "fade", "prune", "rules_process", "store_find"}

// Option configures New. Options are applied in order and are not safe for
// concurrent use; they run once, inside New.
type Option func(*options)

type options struct {
	kinds                  []string
	duration, hops, confid []float64
}

// DefaultDurationBuckets returns the default duration histogram buckets, in
// seconds. It returns a fresh slice and is safe for concurrent use.
func DefaultDurationBuckets() []float64 {
	return []float64{1e-6, 5e-6, 2.5e-5, 1e-4, 5e-4, 2.5e-3, 1e-2, 5e-2, 0.25, 1}
}

// DefaultHopBuckets returns the default delivered-hops histogram buckets. It
// returns a fresh slice and is safe for concurrent use.
func DefaultHopBuckets() []float64 { return []float64{1, 2, 3, 4, 5, 6, 8, 10, 15, 20} }

// DefaultConfidenceBuckets returns the default delivered-confidence histogram
// buckets (0.1 to 1.0). It returns a fresh slice and is safe for concurrent use.
func DefaultConfidenceBuckets() []float64 {
	return []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1}
}

// WithSourceKinds lists the fact source kinds that get their own source label
// on rumour_fact_adds_total; every other kind is labelled "other". The
// argument is copied. The empty string is rejected by New.
func WithSourceKinds(kinds ...string) Option {
	return func(o *options) { o.kinds = append([]string(nil), kinds...) }
}

// WithDurationBuckets overrides the duration histogram buckets (seconds). The
// argument is copied; New rejects an empty, non-finite or non-increasing list.
func WithDurationBuckets(seconds ...float64) Option {
	return func(o *options) { o.duration = append([]float64{}, seconds...) }
}

// WithHopBuckets overrides the delivered-hops histogram buckets. The argument
// is copied; New rejects an empty, non-finite or non-increasing list.
func WithHopBuckets(hops ...float64) Option {
	return func(o *options) { o.hops = append([]float64{}, hops...) }
}

// WithConfidenceBuckets overrides the delivered-confidence histogram buckets.
// The argument is copied; New rejects an empty, non-finite or non-increasing list.
func WithConfidenceBuckets(c ...float64) Option {
	return func(o *options) { o.confid = append([]float64{}, c...) }
}

func checkBuckets(name string, b []float64) error {
	if len(b) == 0 {
		return fmt.Errorf("rumourprom: %s buckets are empty", name)
	}
	for i, v := range b {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("rumourprom: %s buckets contain a non-finite value", name)
		}
		if i > 0 && v <= b[i-1] {
			return fmt.Errorf("rumourprom: %s buckets are not strictly increasing", name)
		}
	}
	return nil
}

// Metrics holds the collectors. Collectors are safe for concurrent use, so one
// Metrics may serve many stores, spreaders, engines and registries.
type Metrics struct {
	calls, offered, spreadErrs, mismatches, evictions, pruned, pruneRuns prometheus.Counter
	fadeExamined, fadeChanged, fadeForgotten, fadeBad                    prometheus.Counter
	inferRuns, inferPasses, inferErrs, inferTrunc                        prometheus.Counter
	rulesProcessed, rulesErrs, rulesUnmatched                            prometheus.Counter
	deliveries, dropped, withheld, derived                               *prometheus.CounterVec
	adds, addErrs, skipped, listenersSkipped, fadeRuns                   *prometheus.CounterVec
	fired, stopped, considered, ruleSkipped, actionErrs, regs, unregs    *prometheus.CounterVec
	registered                                                           *prometheus.GaugeVec
	size, sizeMax                                                        prometheus.Gauge
	hops, confidence                                                     prometheus.Histogram
	dur                                                                  *prometheus.HistogramVec

	kinds map[string]bool

	mu  sync.Mutex // guards the size high-water mark
	max int
}

func counter(name, help string) prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help})
}

func vec(name, help string, labels ...string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
}

func gauge(name, help string) prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
}

// New creates the collectors and registers them with reg. It returns an error
// for an invalid option (empty, non-finite or non-increasing bucket list, empty
// source kind) or a registration failure, for example if New was already called
// on the same reg; on error nothing New created is left registered.
func New(reg prometheus.Registerer, opts ...Option) (*Metrics, error) {
	o := options{duration: DefaultDurationBuckets(), hops: DefaultHopBuckets(), confid: DefaultConfidenceBuckets()}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	for name, b := range map[string][]float64{"duration": o.duration, "hop": o.hops, "confidence": o.confid} {
		if err := checkBuckets(name, b); err != nil {
			return nil, err
		}
	}
	kinds := map[string]bool{}
	var kindList []string
	for _, k := range o.kinds {
		if k == "" {
			return nil, errors.New("rumourprom: empty source kind")
		}
		if k != otherSource && !kinds[k] {
			kinds[k] = true
			kindList = append(kindList, k)
		}
	}
	kindList = append(kindList, otherSource)

	m := &Metrics{
		kinds:            kinds,
		calls:            counter("rumour_spread_calls_total", "Spread calls that ran (a teller with a store)."),
		offered:          counter("rumour_spread_offered_total", "Fact and listener pairs considered by Spread."),
		spreadErrs:       counter("rumour_spread_errors_total", "Spread calls that returned an error."),
		mismatches:       counter("rumour_holder_type_mismatch_total", "Typed filters or transforms given a holder of the wrong type (a wiring bug)."),
		evictions:        counter("rumour_fact_evictions_total", "Facts evicted to honour the store's MaxFacts."),
		pruned:           counter("rumour_fact_pruned_total", "Facts removed by Store.Prune."),
		pruneRuns:        counter("rumour_prune_runs_total", "Store.Prune calls."),
		fadeExamined:     counter("rumour_fade_examined_total", "Facts the Fader was called on."),
		fadeChanged:      counter("rumour_fade_changed_total", "Facts that survived Fade with a different confidence."),
		fadeForgotten:    counter("rumour_fade_forgotten_total", "Facts removed by Fade."),
		fadeBad:          counter("rumour_fade_invalid_total", "Facts whose Fader result was NaN (a buggy Fader)."),
		inferRuns:        counter("rumour_infer_runs_total", "Inference Run calls."),
		inferPasses:      counter("rumour_infer_passes_total", "Inference passes executed."),
		inferErrs:        counter("rumour_infer_errors_total", "Inference Run calls that returned an error."),
		inferTrunc:       counter("rumour_infer_truncated_total", "Inference Run calls that hit MaxPasses while the last pass still changed something."),
		rulesProcessed:   counter("rumour_rules_processed_total", "Rules registry Process calls."),
		rulesErrs:        counter("rumour_rules_errors_total", "Process calls that returned an error."),
		rulesUnmatched:   counter("rumour_rules_unmatched_total", "Process calls where no layer had a candidate rule for the event type."),
		deliveries:       vec("rumour_spread_deliveries_total", "Copies handed to a listener's store, by what the store did.", "outcome"),
		dropped:          vec("rumour_spread_dropped_total", "Offers that did not become a delivery, by reason.", "reason"),
		withheld:         vec("rumour_spread_withheld_total", "Facts the teller held that were not offered at all, by reason (not part of offered or dropped).", "reason"),
		derived:          vec("rumour_infer_derived_total", "Facts derived by inference, by store outcome.", "outcome"),
		adds:             vec("rumour_fact_adds_total", "Store adds that returned no error, by outcome and source kind (a rejected add is still an add).", "outcome", "source"),
		addErrs:          vec("rumour_fact_add_errors_total", "Store.Add calls that returned an error, by reason.", "reason"),
		skipped:          vec("rumour_spread_skipped_total", "Spread calls that returned early, by reason.", "reason"),
		listenersSkipped: vec("rumour_spread_listeners_skipped_total", "Listeners passed to Spread and skipped, by reason.", "reason"),
		fadeRuns:         vec("rumour_fade_runs_total", "Store.Fade calls, by whether a Fader is wired.", "fader"),
		fired:            vec("rumour_rules_fired_total", "Rules fired, by layer.", "layer"),
		stopped:          vec("rumour_rules_stopped_total", "Process calls cut short, by stop mode.", "mode"),
		considered:       vec("rumour_rules_considered_total", "Candidate rules for an event, by layer.", "layer"),
		ruleSkipped:      vec("rumour_rules_skipped_total", "Candidate rules whose When was false, by layer.", "layer"),
		actionErrs:       vec("rumour_rules_action_errors_total", "Fired rules whose Do returned an error, by layer.", "layer"),
		regs:             vec("rumour_rules_register_total", "Register calls, by result.", "result"),
		unregs:           vec("rumour_rules_unregister_total", "Unregister calls, by result.", "result"),
		registered: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "rumour_rules_registered", Help: "Rules currently registered, by layer."}, []string{"layer"}),
		size:       gauge("rumour_store_size", "Fact count of the store last touched by an add, fade or prune."),
		sizeMax:    gauge("rumour_store_size_max", "High-water mark of any observed store size."),
		hops:       prometheus.NewHistogram(prometheus.HistogramOpts{Name: "rumour_spread_delivered_hops", Help: "Hops of delivered copies.", Buckets: o.hops}),
		confidence: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "rumour_spread_delivered_confidence", Help: "Confidence of delivered copies.", Buckets: o.confid}),
		dur: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "rumour_operation_duration_seconds",
			Help: "Wall time per operation; only when a clock is injected.", Buckets: o.duration}, []string{"op"}),
	}
	// Create every closed-set series now so alerts and dashboards see them before the first event.
	for _, oc := range []knowledge.Outcome{knowledge.Rejected, knowledge.Inserted, knowledge.Replaced} {
		m.deliveries.WithLabelValues(oc.String())
		for _, k := range kindList {
			m.adds.WithLabelValues(oc.String(), k)
		}
	}
	spread.Dropped{}.Each(func(reason string, _ int) { m.dropped.WithLabelValues(reason) })
	spread.Withheld{}.Each(func(reason string, _ int) { m.withheld.WithLabelValues(reason) })
	spread.SkippedListeners{}.Each(func(reason string, _ int) { m.listenersSkipped.WithLabelValues(reason) })
	for _, r := range spread.SkipReasons() {
		m.skipped.WithLabelValues(r)
	}
	for _, r := range knowledge.AddErrorReasons() {
		m.addErrs.WithLabelValues(r)
	}
	m.fadeRuns.WithLabelValues("wired")
	m.fadeRuns.WithLabelValues("none")
	m.derived.WithLabelValues(knowledge.Inserted.String()) // rejected cannot occur for a derivation
	m.derived.WithLabelValues(knowledge.Replaced.String())
	m.stopped.WithLabelValues(string(rules.StopLayer))
	m.stopped.WithLabelValues(string(rules.StopAll))
	for _, r := range []string{"ok", "duplicate", "invalid", "unknown_layer"} {
		m.regs.WithLabelValues(r)
	}
	m.unregs.WithLabelValues("removed")
	m.unregs.WithLabelValues("not_found")
	for _, op := range ops {
		m.dur.WithLabelValues(op)
	}

	cs := []prometheus.Collector{
		m.calls, m.offered, m.spreadErrs, m.mismatches, m.evictions, m.pruned, m.pruneRuns,
		m.fadeExamined, m.fadeChanged, m.fadeForgotten, m.fadeBad,
		m.inferRuns, m.inferPasses, m.inferErrs, m.inferTrunc,
		m.rulesProcessed, m.rulesErrs, m.rulesUnmatched,
		m.deliveries, m.dropped, m.withheld, m.derived, m.adds, m.addErrs, m.skipped, m.listenersSkipped,
		m.fadeRuns, m.fired, m.stopped, m.considered, m.ruleSkipped, m.actionErrs, m.regs, m.unregs,
		m.registered, m.size, m.sizeMax, m.hops, m.confidence, m.dur,
	}
	for i, c := range cs {
		if err := reg.Register(c); err != nil {
			for _, done := range cs[:i] { // leave reg as we found it
				reg.Unregister(done)
			}
			return nil, err
		}
	}
	return m, nil
}

func (m *Metrics) observeSize(n int) {
	m.size.Set(float64(n))
	m.mu.Lock()
	if n > m.max {
		m.max = n
		m.sizeMax.Set(float64(n))
	}
	m.mu.Unlock()
}

func (m *Metrics) observeDur(op string, d float64) {
	m.dur.WithLabelValues(op).Observe(math.Max(d, 0))
}

// Spread returns the option that records every Spread call, including holder
// type mismatches (Result.HolderMismatches), early-return skips and, if the
// spreader has a clock, durations. It chains with other spread.WithHooks options.
func (m *Metrics) Spread() spread.Option {
	return spread.WithHooks(spread.Hooks{
		OnSpread: func(r spread.Result, err error) {
			m.calls.Inc()
			m.offered.Add(float64(r.Offered))
			m.mismatches.Add(float64(r.HolderMismatches))
			for _, t := range r.Transfers {
				m.deliveries.WithLabelValues(t.Outcome.String()).Inc()
				if t.Outcome != knowledge.Rejected {
					m.hops.Observe(float64(t.Hops))
					m.confidence.Observe(t.Confidence)
				}
			}
			r.Dropped.Each(func(reason string, n int) { m.dropped.WithLabelValues(reason).Add(float64(n)) })
			r.Withheld.Each(func(reason string, n int) { m.withheld.WithLabelValues(reason).Add(float64(n)) })
			r.SkippedListeners.Each(func(reason string, n int) { m.listenersSkipped.WithLabelValues(reason).Add(float64(n)) })
			if err != nil {
				m.spreadErrs.Inc()
			}
		},
		OnSkip:       func(reason string) { m.skipped.WithLabelValues(reason).Inc() },
		OnSpreadDone: func(d time.Duration) { m.observeDur("spread", d.Seconds()) },
	})
}

// Infer returns the option that records every inference Run (and its duration,
// if the engine has a clock). It chains with other infer.WithHooks options.
func (m *Metrics) Infer() infer.Option {
	return infer.WithHooks(infer.Hooks{
		OnRun: func(r infer.Result, err error) {
			m.inferRuns.Inc()
			m.inferPasses.Add(float64(r.Passes))
			if r.Truncated {
				m.inferTrunc.Inc()
			}
			for _, d := range r.Derived {
				m.derived.WithLabelValues(d.Outcome.String()).Inc()
			}
			if err != nil {
				m.inferErrs.Inc()
			}
		},
		OnRunDone: func(d time.Duration) { m.observeDur("infer_run", d.Seconds()) },
	})
}

// Store returns the option that records every add (whoever made it: add_fact,
// spread delivery or inference), add error, eviction, Prune and Fade, the store
// size, and durations if the store has a clock. It chains with other
// knowledge.WithHooks options.
func (m *Metrics) Store() knowledge.Option {
	return knowledge.WithHooks(knowledge.Hooks{
		OnAdd: func(f knowledge.Fact, r knowledge.AddResult) {
			src := otherSource
			if m.kinds[f.Source] {
				src = f.Source
			}
			m.adds.WithLabelValues(r.Outcome.String(), src).Inc()
			m.evictions.Add(float64(len(r.Evicted)))
			m.observeSize(r.Size)
		},
		OnAddError: func(_ knowledge.Fact, reason string, _ error) { m.addErrs.WithLabelValues(reason).Inc() },
		OnPrune: func(r knowledge.PruneResult) {
			m.pruneRuns.Inc()
			m.pruned.Add(float64(r.Removed))
			m.observeSize(r.Size)
		},
		OnFade: func(r knowledge.FadeResult) {
			if r.Wired {
				m.fadeRuns.WithLabelValues("wired").Inc()
			} else {
				m.fadeRuns.WithLabelValues("none").Inc()
			}
			m.fadeExamined.Add(float64(r.Examined))
			m.fadeChanged.Add(float64(len(r.Changed)))
			m.fadeForgotten.Add(float64(len(r.Forgotten)))
			m.fadeBad.Add(float64(len(r.Invalid)))
			m.observeSize(r.Size)
		},
		OnDuration: func(op string, d time.Duration) {
			switch op {
			case knowledge.OpFind:
				op = "store_find"
			case knowledge.OpFade, knowledge.OpPrune: // label equals the op name
			default:
				return
			}
			m.observeDur(op, d.Seconds())
		},
	})
}

// Rules returns the hooks for a rules registry: registry.Observe(m.Rules()).
// Per-layer series are created on first use; use ObserveRules to seed them.
func (m *Metrics) Rules() rules.Hooks {
	return rules.Hooks{
		OnProcess: func(r rules.Result, err error) {
			m.rulesProcessed.Inc()
			if r.Unmatched() {
				m.rulesUnmatched.Inc()
			}
			for _, f := range r.Fired {
				m.fired.WithLabelValues(f.Layer).Inc()
			}
			for _, l := range r.Layers {
				m.considered.WithLabelValues(l.Layer).Add(float64(l.Candidates))
				m.ruleSkipped.WithLabelValues(l.Layer).Add(float64(l.Skipped))
				m.actionErrs.WithLabelValues(l.Layer).Add(float64(l.Failed))
			}
			if r.Stopped != rules.StopNone {
				m.stopped.WithLabelValues(string(r.Stopped)).Inc()
			}
			if err != nil {
				m.rulesErrs.Inc()
			}
		},
		OnProcessDone: func(d time.Duration) { m.observeDur("rules_process", d.Seconds()) },
		OnRegister: func(e rules.RegisterEvent) {
			res := "ok"
			switch {
			case e.Err == nil:
			case errors.Is(e.Err, rules.ErrDuplicateRule):
				res = "duplicate"
			case errors.Is(e.Err, rules.ErrUnknownLayer):
				res = "unknown_layer"
			default: // ErrInvalidRule and anything unexpected
				res = "invalid"
			}
			m.regs.WithLabelValues(res).Inc()
			if e.Err == nil || e.Count > 0 { // skip unknown layers: caller-defined names must stay bounded
				m.registered.WithLabelValues(e.Layer).Set(float64(e.Count))
			}
		},
		OnUnregister: func(e rules.UnregisterEvent) {
			res := "not_found"
			if e.Removed {
				res = "removed"
			}
			m.unregs.WithLabelValues(res).Inc()
			if e.Removed || e.Count > 0 {
				m.registered.WithLabelValues(e.Layer).Set(float64(e.Count))
			}
		},
	}
}

// RuleRegistry is satisfied by every *rules.Registry[C], whatever C is.
type RuleRegistry interface {
	Layers() []string
	RuleCount(layer string) int
	Observe(rules.Hooks)
}

// ObserveRules seeds every per-layer series at zero from r.Layers(), sets
// rumour_rules_registered to r.RuleCount, then attaches m.Rules() to r. Call it
// once per registry at wiring time.
func (m *Metrics) ObserveRules(r RuleRegistry) {
	for _, l := range r.Layers() {
		m.fired.WithLabelValues(l)
		m.considered.WithLabelValues(l)
		m.ruleSkipped.WithLabelValues(l)
		m.actionErrs.WithLabelValues(l)
		m.registered.WithLabelValues(l).Set(float64(r.RuleCount(l)))
	}
	r.Observe(m.Rules())
}
