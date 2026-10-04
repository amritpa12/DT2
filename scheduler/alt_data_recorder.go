package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var altDataRecorderProbeArgv = []string{"--probe-only"}

type altDataRecorderFetchFunc func(ctx context.Context, sources []string) ([]byte, []byte, error)

type altDataRecorderWorker struct {
	mu       sync.Mutex
	cfg      *AltDataRecorderConfig
	store    *StateStore
	notifier *MultiNotifier
	fetch    altDataRecorderFetchFunc
	now      func() time.Time

	lastSuccess time.Time
	lastAlertAt time.Time
	failCount   int
}

func newAltDataRecorderWorker(store *StateStore, notifier *MultiNotifier, fetch altDataRecorderFetchFunc) *altDataRecorderWorker {
	if fetch == nil {
		fetch = runAltDataRecorderFetch
	}
	return &altDataRecorderWorker{
		store:    store,
		notifier: notifier,
		fetch:    fetch,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (w *altDataRecorderWorker) setConfig(cfg *AltDataRecorderConfig) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.cfg = cloneAltDataRecorderConfig(cfg)
	w.mu.Unlock()
}

func (w *altDataRecorderWorker) snapshotConfig() *AltDataRecorderConfig {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return cloneAltDataRecorderConfig(w.cfg)
}

func (w *altDataRecorderWorker) run(ctx context.Context) {
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
			if cfg != nil && cfg.Enabled {
				w.tick(ctx, cfg)
			}
			interval := resolveAltDataRecorderInterval(cfg)
			timer.Reset(interval)
		}
	}
}

func (w *altDataRecorderWorker) tick(ctx context.Context, cfg *AltDataRecorderConfig) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	sources := resolveAltDataSources(cfg)
	stdout, stderr, err := w.fetch(ctx, sources)
	if err != nil {
		w.recordFailure(fmt.Sprintf("%v%s", err, formatAltDataStderr(stderr)))
		return
	}
	rows, err := parseAltDataRecorderPayload(stdout)
	if err != nil {
		w.recordFailure(err.Error())
		return
	}
	stamped := stampReadingsObservedAt(rows, w.now())
	inserted := 0
	for _, row := range stamped {
		if strings.TrimSpace(row.SourceID) == "" {
			continue
		}
		ok, insErr := w.store.InsertAltDataReading(row)
		if insErr != nil {
			w.recordFailure(insErr.Error())
			return
		}
		if ok {
			inserted++
		}
	}
	w.recordSuccess()
	if inserted > 0 {
		fmt.Printf("[alt-data] archived %d new reading(s) (sources=%s)\n", inserted, strings.Join(sources, ","))
	}
}

func (w *altDataRecorderWorker) recordSuccess() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastSuccess = w.now()
	w.failCount = 0
}

func (w *altDataRecorderWorker) recordFailure(detail string) {
	w.mu.Lock()
	w.failCount++
	count := w.failCount
	shouldAlert := w.lastAlertAt.IsZero() || w.now().Sub(w.lastAlertAt) >= effectiveAlertThrottleInterval()
	if shouldAlert {
		w.lastAlertAt = w.now()
	}
	w.mu.Unlock()
	fmt.Fprintf(os.Stderr, "[alt-data] WARN: recorder fetch failed (#%d): %s\n", count, detail)
	if shouldAlert && w.notifier != nil && w.notifier.HasOwner() {
		w.notifier.SendOwnerDM(fmt.Sprintf("**ALT-DATA RECORDER FAILED** (failure #%d): %s. Archive is diagnostics-only; trading is unchanged.", count, detail))
	}
}

func formatAltDataStderr(stderr []byte) string {
	s := strings.TrimSpace(string(stderr))
	if s == "" {
		return ""
	}
	if len(s) > 240 {
		s = s[:240]
	}
	return " / " + s
}

func runAltDataRecorderFetch(ctx context.Context, sources []string) ([]byte, []byte, error) {
	args := []string{"--sources", strings.Join(sources, ",")}
	return spawnPythonProcess(ctx, altDataRecorderScript, args, nil, 45*time.Second)
}

var altDataRecorderApplyConfig func(*AltDataRecorderConfig)

func applyAltDataRecorderHotReload(cfg *AltDataRecorderConfig) {
	if altDataRecorderApplyConfig != nil {
		altDataRecorderApplyConfig(cfg)
	}
}
