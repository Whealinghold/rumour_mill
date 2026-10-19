package knowledge

import (
	"maps"
	"math"
	"slices"
)

// Fader decides how a fact weakens with time. f is a copy of the whole fact
// (including Private); elapsed is the ticks since the fact was last settled,
// never negative. It returns the new confidence and whether to forget the fact
// outright. It runs synchronously on the caller's goroutine, must not call back
// into the same Store, and a panic propagates.
type Fader func(f Fact, now, elapsed Tick) (confidence float64, forget bool)

// FadeResult lists what one Fade call did, each slice in ascending ID order.
// Invalid and Examined are diagnostics: Invalid exposes a buggy Fader that
// silently stops fading some facts, Examined tells "fading is off" from "nothing
// needed to change".
// It is a plain value owned by the caller, shares nothing with the store, and
// is safe to use from any goroutine once Fade has returned (Fade itself is not
// safe for concurrent use against a shared store).
type FadeResult struct {
	Changed   []string // survived with a different confidence; forgotten IDs are not repeated here
	Forgotten []string // IDs removed from the store
	Invalid   []string // IDs whose Fader result was NaN, so they were left unchanged and unsettled
	Examined  int      // unexpired facts the Fader was called on; 0 with a non-empty store means fading is not wired up
	Wired     bool     // a Fader is in effect; false means Fade did nothing
	Size      int      // Len() after the Fade
}

// WithFader sets the Fader used by Fade, replacing the one built from
// Config.Fade. A nil Fader is ignored. The Fader runs inside Fade on the
// caller's goroutine, so it is not safe for concurrent use against a shared
// store and must not call back into the store it is attached to.
func WithFader(f Fader) Option {
	return func(s *Store) {
		if f != nil {
			s.fader = f
		}
	}
}

// Fade applies the store's Fader to every unexpired fact, weakening or
// forgetting it. Without a Fader it does nothing. Expired facts are left alone
// until Prune; Fade never prunes or evicts. A fact is settled at
// max(Observed, FadedAt), so Fade(100) then Fade(200) equals one Fade(200) for
// a Fader that composes. A NaN result leaves the fact unchanged; otherwise
// confidence is clamped to [0,1] and the fact is forgotten if the Fader says
// so or the result is below Config.Fade.ForgetBelow. Only Confidence and
// FadedAt are modified. Not safe for concurrent use.
func (s *Store) Fade(now Tick) FadeResult {
	if s.clock != nil {
		start := s.clock()
		defer func() { s.duration(OpFade, start) }()
	}
	var res FadeResult
	if s.fader == nil {
		res.Size = len(s.facts)
		s.log.Debug("knowledge: fade", "wired", false, "size", res.Size)
		s.fireFade(res)
		return res
	}
	res.Wired = true
	for _, id := range slices.Sorted(maps.Keys(s.facts)) {
		f := s.facts[id]
		if expired(f, now) {
			continue
		}
		res.Examined++
		c, forget := s.fader(clone(f), now, elapsedSince(max(f.Observed, f.FadedAt), now))
		if math.IsNaN(c) {
			res.Invalid = append(res.Invalid, id)
			continue
		}
		c = min(max(c, 0), 1)
		if forget || c < s.cfg.Fade.ForgetBelow {
			delete(s.facts, id)
			res.Forgotten = append(res.Forgotten, id)
			continue
		}
		old := f.Confidence
		f.Confidence, f.FadedAt = c, max(f.FadedAt, now)
		s.facts[id] = f
		if c != old {
			res.Changed = append(res.Changed, id)
		}
	}
	res.Size = len(s.facts)
	s.log.Debug("knowledge: fade", "wired", true, "size", res.Size, "examined", res.Examined, "changed", len(res.Changed), "forgotten", len(res.Forgotten), "invalid", len(res.Invalid))
	if len(res.Invalid) > 0 {
		s.log.Warn("knowledge: fader returned NaN", "invalid", len(res.Invalid), "ids", res.Invalid[:min(len(res.Invalid), 5)])
	}
	s.fireFade(res)
	return res
}

func (s *Store) fireFade(res FadeResult) {
	for _, h := range s.hooks {
		if h.OnFade != nil {
			h.OnFade(res)
		}
	}
}

// elapsedSince returns now-settled, floored at 0 and saturating at MaxInt64.
func elapsedSince(settled, now Tick) Tick {
	if now <= settled {
		return 0
	}
	if d := now - settled; d > 0 {
		return d
	}
	return math.MaxInt64
}

// exponentialFader builds the built-in half-life policy, or nil when no
// half-life is positive so Fade stays a no-op.
func exponentialFader(cfg FadeConfig) Fader {
	if cfg.DefaultHalfLife <= 0 && !slices.ContainsFunc(slices.Collect(maps.Values(cfg.HalfLives)), func(h int64) bool { return h > 0 }) {
		return nil
	}
	return func(f Fact, _, elapsed Tick) (float64, bool) {
		h, ok := cfg.HalfLives[f.Source]
		if !ok {
			h = cfg.DefaultHalfLife
		}
		if h <= 0 {
			return f.Confidence, false
		}
		return f.Confidence * math.Pow(0.5, float64(elapsed)/float64(h)), false
	}
}
