package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// EventRiskGateConfig is the DEFAULT-OFF per-strategy hold. Missing or
// enabled=false is a no-op. It never force-closes.
type EventRiskGateConfig struct {
	Enabled    bool     `json:"enabled"`
	EventTypes []string `json:"event_types,omitempty"`
	MaxAge     string   `json:"max_age,omitempty"`
	OnFailure  string   `json:"on_failure,omitempty"`
}

type EventRiskGateState struct {
	RiskState string `json:"risk_state,omitempty"`
	Detail    string `json:"detail,omitempty"`
	EventKey  string `json:"event_key,omitempty"`
	Notified  string `json:"notified,omitempty"`
	LastOpen  string `json:"last_open,omitempty"`
	Observed  bool   `json:"observed,omitempty"`
	Active    bool   `json:"active,omitempty"`
}

type EventRiskDecision struct {
	Active bool
	Holds  bool
	State  string
	Key    string
	Detail string
}

func eventRiskGateConfigured(sc StrategyConfig) bool {
	return sc.EventRiskGate != nil && sc.EventRiskGate.Enabled
}

func cloneEventRiskGateConfig(c *EventRiskGateConfig) *EventRiskGateConfig {
	if c == nil {
		return nil
	}
	out := *c
	if c.EventTypes != nil {
		out.EventTypes = append([]string(nil), c.EventTypes...)
	}
	return &out
}

func formatEventRiskGateForLog(c *EventRiskGateConfig) string {
	if c == nil {
		return "(unset)"
	}
	if !c.Enabled {
		return "disabled"
	}
	return fmt.Sprintf("enabled types=%v max_age=%q on_failure=%q", c.EventTypes, c.MaxAge, c.OnFailure)
}

func eventRiskGateConfigsEqual(a, b *EventRiskGateConfig) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Enabled != b.Enabled || a.MaxAge != b.MaxAge || a.OnFailure != b.OnFailure {
		return false
	}
	if len(a.EventTypes) != len(b.EventTypes) {
		return false
	}
	for i := range a.EventTypes {
		if a.EventTypes[i] != b.EventTypes[i] {
			return false
		}
	}
	return true
}

func parseEventRiskOnFailure(v string) (string, error) {
	switch n := strings.ToLower(strings.TrimSpace(v)); n {
	case "", eventRiskOnFailureOpen, eventRiskOnFailureClosed:
		return n, nil
	}
	return "", fmt.Errorf("event_risk_gate.on_failure must be %q or %q, got %q", eventRiskOnFailureOpen, eventRiskOnFailureClosed, v)
}

func resolveEventRiskOnFailure(sc StrategyConfig) string {
	if sc.EventRiskGate != nil {
		if v := strings.ToLower(strings.TrimSpace(sc.EventRiskGate.OnFailure)); v != "" {
			return v
		}
	}
	return eventRiskOnFailureOpen
}

func resolveEventRiskMaxAge(sc StrategyConfig) time.Duration {
	if sc.EventRiskGate == nil || strings.TrimSpace(sc.EventRiskGate.MaxAge) == "" {
		return eventRiskDefaultMaxAge
	}
	d, err := time.ParseDuration(strings.TrimSpace(sc.EventRiskGate.MaxAge))
	if err != nil || d <= 0 {
		return eventRiskDefaultMaxAge
	}
	return d
}

func resolveEventRiskTypes(sc StrategyConfig) []string {
	if sc.EventRiskGate == nil || len(sc.EventRiskGate.EventTypes) == 0 {
		return append([]string(nil), eventRiskHoldTypes...)
	}
	out := make([]string, 0, len(sc.EventRiskGate.EventTypes))
	seen := make(map[string]bool)
	for _, raw := range sc.EventRiskGate.EventTypes {
		t := normalizeEventRiskType(raw)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func (s EventRiskGateState) IsZero() bool {
	return s.RiskState == "" && s.Detail == "" && !s.Observed && !s.Active
}

func marshalEventRiskGateStateJSON(st EventRiskGateState) string {
	if st.IsZero() {
		return ""
	}
	b, err := json.Marshal(st)
	if err != nil {
		return ""
	}
	return string(b)
}

func unmarshalEventRiskGateStateJSON(raw string) EventRiskGateState {
	if strings.TrimSpace(raw) == "" {
		return EventRiskGateState{}
	}
	var st EventRiskGateState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return EventRiskGateState{}
	}
	return st
}

func evaluateEventRiskGate(sc StrategyConfig, events []AltDataEvent, now time.Time, posQty float64, feedErr error, feedStale bool) EventRiskDecision {
	d := EventRiskDecision{State: eventRiskStateClear}
	if !eventRiskGateConfigured(sc) {
		return d
	}
	d.Active = true
	failClosed := resolveEventRiskOnFailure(sc) == eventRiskOnFailureClosed
	if feedErr != nil || feedStale {
		if failClosed && posQty <= 0 {
			d.Holds = true
			d.State = eventRiskStateHold
			if feedErr != nil {
				d.Detail = "event-risk feed unreadable, fail-closed (flat-only)"
			} else {
				d.Detail = "event-risk feed stale, fail-closed (flat-only)"
			}
			return d
		}
		d.Detail = "event-risk feed unreadable, fail-open"
		if feedStale && feedErr == nil {
			d.Detail = "event-risk feed stale, fail-open"
		}
		return d
	}
	eval := evaluateEventRisk(events, extractAsset(sc), resolveEventRiskTypes(sc), now, resolveEventRiskMaxAge(sc))
	d.State = eval.State
	d.Holds = eval.Holds
	d.Key = eval.Key
	d.Detail = eval.Detail
	return d
}

func advanceEventRiskGate(sc StrategyConfig, store *StateStore, stratState *StrategyState, mu *sync.RWMutex, posQty float64) EventRiskDecision {
	if !eventRiskGateConfigured(sc) {
		return EventRiskDecision{State: eventRiskStateClear}
	}
	now := time.Now().UTC()
	var feedErr error
	var events []AltDataEvent
	if store == nil {
		feedErr = fmt.Errorf("state store unavailable")
	} else {
		events, feedErr = store.LoadRecentAltDataEvents(200)
	}
	stale := eventRiskFeedIsStale(resolveEventRiskInterval(nil))
	d := evaluateEventRiskGate(sc, events, now, posQty, feedErr, stale)
	if stratState != nil {
		if mu != nil {
			mu.Lock()
		}
		next := stratState.EventRiskGate
		next.Active = d.Active
		next.RiskState = d.State
		next.Detail = d.Detail
		next.EventKey = d.Key
		next.Observed = feedErr == nil
		stratState.EventRiskGate = next
		if mu != nil {
			mu.Unlock()
		}
	}
	return d
}

func applyEventRiskGateHold(sc StrategyConfig, store *StateStore, stratState *StrategyState, mu *sync.RWMutex, posQty float64, signal *int, closeFraction float64, posSide string, allowsLong, allowsShort bool) EventRiskDecision {
	d := advanceEventRiskGate(sc, store, stratState, mu, posQty)
	if d.Holds && signal != nil && pausedBlocksSignal(*signal, closeFraction, posQty, posSide, allowsLong, allowsShort) {
		*signal = 0
	}
	return d
}

func stampEventRiskAtOpenIfOpened(s *StrategyState, symbol string, opened bool) {
	if s == nil || !opened || !s.EventRiskGate.Active {
		return
	}
	pos, ok := s.Positions[symbol]
	if !ok || pos == nil {
		return
	}
	pos.EventRiskAtOpen = s.EventRiskGate.RiskState
	s.EventRiskGate.LastOpen = s.EventRiskGate.RiskState
}

func validateEventRiskGateConfigs(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	var errs []string
	for _, sc := range cfg.Strategies {
		g := sc.EventRiskGate
		if g == nil {
			continue
		}
		prefix := fmt.Sprintf("strategy[%s]", sc.ID)
		if _, err := parseEventRiskOnFailure(g.OnFailure); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", prefix, err))
		}
		if strings.TrimSpace(g.MaxAge) != "" {
			d, err := time.ParseDuration(strings.TrimSpace(g.MaxAge))
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: event_risk_gate.max_age: invalid duration %q: %v", prefix, g.MaxAge, err))
			} else if d <= 0 {
				errs = append(errs, fmt.Sprintf("%s: event_risk_gate.max_age must be > 0, got %s", prefix, g.MaxAge))
			}
		}
		for _, raw := range g.EventTypes {
			if _, err := parseEventRiskType(raw); err != nil {
				errs = append(errs, fmt.Sprintf("%s: event_risk_gate.event_types: %v", prefix, err))
			}
		}
		switch sc.Type {
		case "options", "manual":
			if g.Enabled {
				errs = append(errs, fmt.Sprintf("%s: event_risk_gate is not supported for type=%q — the gate wires into the spot/perps/futures signal dispatch only", prefix, sc.Type))
			}
		}
	}
	sort.Strings(errs)
	return errs
}
