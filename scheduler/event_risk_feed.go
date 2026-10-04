package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const eventRiskFeedScript = "shared_tools/event_risk_feed.py"

var eventRiskFeedProbeArgv = []string{"--probe-only"}

type eventRiskFeedFetchFunc func(ctx context.Context, sources []string) ([]byte, []byte, error)

type eventRiskFeedWorker struct {
	mu       sync.Mutex
	cfg      *Config
	store    *StateStore
	notifier *MultiNotifier
	fetch    eventRiskFeedFetchFunc
	now      func() time.Time

	lastSuccess time.Time
	lastAlertAt time.Time
	failCount   int
}

func newEventRiskFeedWorker(store *StateStore, notifier *MultiNotifier, fetch eventRiskFeedFetchFunc) *eventRiskFeedWorker {
	if fetch == nil {
		fetch = runEventRiskFeedFetch
	}
	return &eventRiskFeedWorker{
		store:    store,
		notifier: notifier,
		fetch:    fetch,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (w *eventRiskFeedWorker) setConfig(cfg *Config) {
	if w == nil {
		return
	}
	w.mu.Lock()
	if cfg == nil {
		w.cfg = nil
	} else {
		cp := *cfg
		cp.EventRisk = cloneEventRiskConfig(cfg.EventRisk)
		cp.Strategies = append([]StrategyConfig(nil), cfg.Strategies...)
		for i := range cp.Strategies {
			cp.Strategies[i].EventRiskGate = cloneEventRiskGateConfig(cfg.Strategies[i].EventRiskGate)
		}
		w.cfg = &cp
	}
	w.mu.Unlock()
}

func (w *eventRiskFeedWorker) snapshotConfig() *Config {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cfg == nil {
		return nil
	}
	cp := *w.cfg
	cp.EventRisk = cloneEventRiskConfig(w.cfg.EventRisk)
	return &cp
}

func (w *eventRiskFeedWorker) lastSuccessAt() time.Time {
	if w == nil {
		return time.Time{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastSuccess
}

func (w *eventRiskFeedWorker) run(ctx context.Context) {
	if w == nil {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			cfg := w.snapshotConfig()
			if eventRiskPollerEnabled(cfg) {
				w.tick(ctx, cfg)
			}
			interval := resolveEventRiskInterval(nil)
			if cfg != nil {
				interval = resolveEventRiskInterval(cfg.EventRisk)
			}
			timer.Reset(interval)
		}
	}
}

func (w *eventRiskFeedWorker) tick(ctx context.Context, cfg *Config) {
	if !eventRiskPollerEnabled(cfg) {
		return
	}
	sources := resolveEventRiskSources(nil)
	if cfg != nil {
		sources = resolveEventRiskSources(cfg.EventRisk)
	}
	stdout, stderr, err := w.fetch(ctx, sources)
	if err != nil {
		w.recordFailure(fmt.Sprintf("%v%s", err, formatAltDataStderr(stderr)))
		return
	}
	rows, err := parseEventRiskFeedPayload(stdout)
	if err != nil {
		w.recordFailure(err.Error())
		return
	}
	stamped := markSyndicatedCopies(stampEventsObservedAt(rows, w.now()))
	inserted := 0
	activeBySourceType := map[string]map[string]bool{}
	for _, row := range stamped {
		if strings.TrimSpace(row.SourceID) == "" || strings.TrimSpace(row.EventType) == "" {
			continue
		}
		if row.Tier == eventRiskTierT0 && eventTypeCanHold(row.EventType) {
			row.State = eventRiskStateHold
		}
		key := row.SourceID + "|" + row.EventType
		if activeBySourceType[key] == nil {
			activeBySourceType[key] = map[string]bool{}
		}
		if row.Asset != "" {
			activeBySourceType[key][row.Asset] = true
		}
		ok, insErr := w.store.InsertAltDataEvent(row)
		if insErr != nil {
			w.recordFailure(insErr.Error())
			return
		}
		if ok {
			inserted++
			w.notifyEventTransition(row)
		}
	}
	for key, assets := range activeBySourceType {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			continue
		}
		if _, err := w.store.RetractAltDataEventsMissing(parts[0], parts[1], assets); err != nil {
			w.recordFailure(err.Error())
			return
		}
	}
	w.recordSuccess()
	if inserted > 0 {
		fmt.Printf("[event-risk] archived %d new event(s) (sources=%s)\n", inserted, strings.Join(sources, ","))
	}
}

func (w *eventRiskFeedWorker) recordSuccess() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastSuccess = w.now()
	w.failCount = 0
}

func (w *eventRiskFeedWorker) recordFailure(detail string) {
	w.mu.Lock()
	w.failCount++
	count := w.failCount
	shouldAlert := w.lastAlertAt.IsZero() || w.now().Sub(w.lastAlertAt) >= effectiveAlertThrottleInterval()
	if shouldAlert {
		w.lastAlertAt = w.now()
	}
	w.mu.Unlock()
	fmt.Fprintf(os.Stderr, "[event-risk] WARN: feed fetch failed (#%d): %s\n", count, detail)
	if shouldAlert && w.notifier != nil && w.notifier.HasOwner() {
		w.notifier.SendOwnerDM(fmt.Sprintf("**EVENT-RISK FEED FAILED** (failure #%d): %s. Gate is hold-only; open positions are not force-closed.", count, detail))
	}
}

func (w *eventRiskFeedWorker) notifyEventTransition(row AltDataEvent) {
	if w == nil || w.notifier == nil || !w.notifier.HasOwner() {
		return
	}
	switch row.State {
	case eventRiskStateHold:
		w.notifier.SendOwnerDM(fmt.Sprintf("**EVENT-RISK HOLD** %s %s via %s (%s). New position-increasing signals are held; open books are not force-closed.",
			row.EventType, row.Asset, row.SourceID, row.Tier))
	case eventRiskStateWatch:
		w.notifier.SendOwnerDM(fmt.Sprintf("**EVENT-RISK WATCH** %s %s via %s. Unconfirmed; nothing is blocked.",
			row.EventType, row.Asset, row.SourceID))
	}
}

func runEventRiskFeedFetch(ctx context.Context, sources []string) ([]byte, []byte, error) {
	args := []string{"--sources", strings.Join(sources, ",")}
	return spawnPythonProcess(ctx, eventRiskFeedScript, args, nil, 45*time.Second)
}

var eventRiskFeedApplyConfig func(*Config)
var eventRiskFeedLastSuccess func() time.Time

func applyEventRiskHotReload(cfg *Config) {
	if eventRiskFeedApplyConfig != nil {
		eventRiskFeedApplyConfig(cfg)
	}
}

func eventRiskFeedIsStale(interval time.Duration) bool {
	if eventRiskFeedLastSuccess == nil {
		return false
	}
	last := eventRiskFeedLastSuccess()
	if last.IsZero() {
		return false
	}
	if interval <= 0 {
		interval = eventRiskDefaultInterval
	}
	return time.Since(last) > 2*interval
}
