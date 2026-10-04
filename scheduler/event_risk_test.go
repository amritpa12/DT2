package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func testEvent(source, tier, asset, eventType, observed string) AltDataEvent {
	ts, _ := parseEventRiskTime(observed)
	if ts.IsZero() {
		ts = time.Date(2026, 10, 4, 22, 0, 0, 0, time.UTC)
	}
	return AltDataEvent{
		SourceID:        source,
		Tier:            tier,
		Asset:           strings.ToUpper(asset),
		EventType:       eventType,
		VenueOrProtocol: "hyperliquid",
		ObservedAt:      ts.Format(time.RFC3339Nano),
		CanonicalKey:    eventRiskCanonicalKey(asset, eventType, "hyperliquid", ts),
		State:           eventRiskStateWatch,
		Title:           eventType + " " + asset,
	}
}

func gateSC(onFailure string) StrategyConfig {
	return StrategyConfig{
		ID:   "s1",
		Type: "perps",
		Args: []string{"tema", "BTC", "1h"},
		EventRiskGate: &EventRiskGateConfig{
			Enabled:   true,
			OnFailure: onFailure,
		},
	}
}

func TestEventRiskT0AloneHoldsAndT2NeverDoes(t *testing.T) {
	now := time.Date(2026, 10, 4, 22, 30, 0, 0, time.UTC)
	t0 := testEvent("hl_meta", eventRiskTierT0, "BTC", eventRiskTypeDelist, "2026-10-04T22:10:00Z")
	t0.State = eventRiskStateHold
	eval := evaluateEventRisk([]AltDataEvent{t0}, "BTC", nil, now, eventRiskDefaultMaxAge)
	if !eval.Holds || eval.State != eventRiskStateHold {
		t.Fatalf("T0 must hold alone: %+v", eval)
	}
	t2 := testEvent("fng", eventRiskTierT2, "BTC", eventRiskTypeExploit, "2026-10-04T22:10:00Z")
	eval = evaluateEventRisk([]AltDataEvent{t2}, "BTC", nil, now, eventRiskDefaultMaxAge)
	if eval.Holds || eval.State != eventRiskStateClear {
		t.Fatalf("T2 must never hold: %+v", eval)
	}
}

func TestEventRiskT1NeedsConfirmation(t *testing.T) {
	now := time.Date(2026, 10, 4, 22, 30, 0, 0, time.UTC)
	one := testEvent("coindesk", eventRiskTierT1, "BTC", eventRiskTypeExploit, "2026-10-04T22:10:00Z")
	one.Title = "Protocol reports a treasury drain after a bridge exploit"
	eval := evaluateEventRisk([]AltDataEvent{one}, "BTC", nil, now, eventRiskDefaultMaxAge)
	if eval.Holds || eval.State != eventRiskStateWatch {
		t.Fatalf("single T1 is watch only: %+v", eval)
	}
	two := testEvent("theblock", eventRiskTierT1, "BTC", eventRiskTypeExploit, "2026-10-04T22:20:00Z")
	two.Title = "Investigators confirm attacker emptied the validator set"
	eval = evaluateEventRisk([]AltDataEvent{one, two}, "BTC", nil, now, eventRiskDefaultMaxAge)
	if !eval.Holds || eval.State != eventRiskStateHold {
		t.Fatalf("two independent T1 within 2h must hold: %+v", eval)
	}
	late := testEvent("reuters", eventRiskTierT1, "BTC", eventRiskTypeExploit, "2026-10-04T18:00:00Z")
	late.Title = "Earlier same-day note from a different newsroom"
	eval = evaluateEventRisk([]AltDataEvent{one, late}, "BTC", nil, now, eventRiskDefaultMaxAge)
	if eval.Holds {
		t.Fatalf("T1 pair outside 2h must not confirm: %+v", eval)
	}
	withFact := one
	withFact.MarketFact = true
	eval = evaluateEventRisk([]AltDataEvent{withFact}, "BTC", nil, now, eventRiskDefaultMaxAge)
	if !eval.Holds {
		t.Fatalf("T1 + market fact must hold: %+v", eval)
	}
}

func TestEventRiskSyndicatedCopiesDoNotConfirm(t *testing.T) {
	now := time.Date(2026, 10, 4, 22, 30, 0, 0, time.UTC)
	orig := testEvent("reuters", eventRiskTierT1, "ETH", eventRiskTypeExploit, "2026-10-04T22:10:00Z")
	orig.Title = "Bridge exploit drains protocol treasury"
	copy1 := testEvent("outlet", eventRiskTierT1, "ETH", eventRiskTypeExploit, "2026-10-04T22:15:00Z")
	copy1.Title = "Bridge exploit drains protocol treasury"
	copy1.SyndicateOf = "reuters"
	eval := evaluateEventRisk([]AltDataEvent{orig, copy1}, "ETH", nil, now, eventRiskDefaultMaxAge)
	if eval.Holds || eval.State != eventRiskStateWatch {
		t.Fatalf("syndicated T1 must not count as confirmation: %+v", eval)
	}
	sameOwner := testEvent("reuters:wire", eventRiskTierT1, "ETH", eventRiskTypeExploit, "2026-10-04T22:16:00Z")
	sameOwner.Title = "Different wording about an unrelated listing"
	eval = evaluateEventRisk([]AltDataEvent{orig, sameOwner}, "ETH", nil, now, eventRiskDefaultMaxAge)
	if eval.Holds {
		t.Fatalf("same-owner T1 must not confirm: %+v", eval)
	}
	similar := testEvent("theblock", eventRiskTierT1, "ETH", eventRiskTypeExploit, "2026-10-04T22:18:00Z")
	similar.Title = "Bridge exploit drains protocol treasury"
	marked := markSyndicatedCopies([]AltDataEvent{orig, similar})
	if marked[1].SyndicateOf != "reuters" {
		t.Fatalf("Jaccard>=0.8 inside 6h must collapse to the first source, got %+v", marked[1])
	}
}

func TestEventRiskRetractionAndExpiryLiftHold(t *testing.T) {
	now := time.Date(2026, 10, 4, 22, 30, 0, 0, time.UTC)
	delist := testEvent("hl_meta", eventRiskTierT0, "ABC", eventRiskTypeDelist, "2026-10-03T22:10:00Z")
	delist.State = eventRiskStateHold
	if eval := evaluateEventRisk([]AltDataEvent{delist}, "ABC", nil, now, eventRiskDefaultMaxAge); !eval.Holds {
		t.Fatalf("halt/delist must not auto-expire: %+v", eval)
	}
	delist.State = eventRiskStateRetracted
	if eval := evaluateEventRisk([]AltDataEvent{delist}, "ABC", nil, now, eventRiskDefaultMaxAge); eval.Holds {
		t.Fatalf("retracted T0 must lift: %+v", eval)
	}
	depeg := testEvent("peg", eventRiskTierT0, "USDT", eventRiskTypeDepeg, "2026-10-03T21:00:00Z")
	depeg.State = eventRiskStateHold
	if eval := evaluateEventRisk([]AltDataEvent{depeg}, "USDT", nil, now, eventRiskDefaultMaxAge); eval.Holds {
		t.Fatalf("depeg older than max_age must expire: %+v", eval)
	}
}

func TestEventRiskFailClosedIsFlatOnly(t *testing.T) {
	sc := gateSC(eventRiskOnFailureClosed)
	feedErr := errString("db down")
	flat := evaluateEventRiskGate(sc, nil, time.Now().UTC(), 0, feedErr, false)
	if !flat.Holds {
		t.Fatalf("fail-closed must hold when flat: %+v", flat)
	}
	open := evaluateEventRiskGate(sc, nil, time.Now().UTC(), 2, feedErr, false)
	if open.Holds {
		t.Fatalf("fail-closed must not hold an already-open book: %+v", open)
	}
	failOpen := evaluateEventRiskGate(gateSC(eventRiskOnFailureOpen), nil, time.Now().UTC(), 0, feedErr, false)
	if failOpen.Holds {
		t.Fatalf("fail-open must not hold: %+v", failOpen)
	}
}

func TestEventRiskHoldBlocksIncreaseNotClose(t *testing.T) {
	d := EventRiskDecision{Active: true, Holds: true, State: eventRiskStateHold}
	if !d.Holds || !pausedBlocksSignal(1, 0, 0, "", true, true) {
		t.Fatal("hold must block a flat open")
	}
	if pausedBlocksSignal(-1, 1, 2, "long", true, true) {
		t.Fatal("hold must let a close pass")
	}
	if pausedBlocksSignal(-1, 0.5, 2, "long", true, true) {
		t.Fatal("hold must let a partial close pass")
	}
	sig := 1
	apply := applyEventRiskGateHold
	_ = apply
	held := d.Holds && pausedBlocksSignal(sig, 0, 0, "", true, false)
	if !held {
		t.Fatal("position-increasing signal must be held")
	}
}

func TestStampEventsObservedAtOverwritesVendorClock(t *testing.T) {
	now := time.Date(2026, 10, 4, 22, 15, 30, 123456789, time.UTC)
	rows := stampEventsObservedAt([]AltDataEvent{{
		SourceID:    "HL_META",
		Asset:       "abc",
		EventType:   "DELIST",
		PublishedAt: "2026-10-04T00:00:00Z",
		ObservedAt:  "2018-01-01T00:00:00Z",
		Title:       "delist",
	}}, now)
	if rows[0].ObservedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("observed_at must be our receipt clock, got %q", rows[0].ObservedAt)
	}
	if rows[0].PublishedAt != "2026-10-04T00:00:00Z" {
		t.Fatalf("published_at must keep the vendor stamp, got %q", rows[0].PublishedAt)
	}
	if rows[0].CanonicalKey == "" || rows[0].SourceID != "hl_meta" || rows[0].Asset != "ABC" {
		t.Fatalf("normalize: %+v", rows[0])
	}
}

func TestEventRiskInsertDedupAndRetract(t *testing.T) {
	store := openTestStore(t, openTestDB(t))
	now := time.Date(2026, 10, 4, 22, 0, 0, 0, time.UTC)
	row := stampEventsObservedAt([]AltDataEvent{{
		SourceID:  "hl_meta",
		Tier:      eventRiskTierT0,
		Asset:     "ABC",
		EventType: eventRiskTypeDelist,
		State:     eventRiskStateHold,
		Title:     "delist ABC",
	}}, now)[0]
	first, err := store.InsertAltDataEvent(row)
	if err != nil || !first {
		t.Fatalf("first insert: inserted=%v err=%v", first, err)
	}
	second, err := store.InsertAltDataEvent(row)
	if err != nil {
		t.Fatal(err)
	}
	if second {
		t.Fatal("same source+canonical_key must not insert twice")
	}
	n, err := store.RetractAltDataEventsMissing("hl_meta", eventRiskTypeDelist, map[string]bool{})
	if err != nil || n != 1 {
		t.Fatalf("retract missing: n=%d err=%v", n, err)
	}
	got, err := store.LoadRecentAltDataEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].State != eventRiskStateRetracted {
		t.Fatalf("want retracted row, got %+v", got)
	}
}

func TestEventRiskFeedDisabledIsNoOp(t *testing.T) {
	store := openTestStore(t, openTestDB(t))
	fetched := 0
	w := newEventRiskFeedWorker(store, nil, func(context.Context, []string) ([]byte, []byte, error) {
		fetched++
		return []byte(`{"events":[]}`), nil, nil
	})
	w.setConfig(&Config{EventRisk: &EventRiskConfig{Enabled: false}})
	w.tick(context.Background(), w.snapshotConfig())
	if fetched != 0 {
		t.Fatalf("disabled poller must not fetch, got %d", fetched)
	}
}

func TestEventRiskFeedTickStampsObservedAt(t *testing.T) {
	store := openTestStore(t, openTestDB(t))
	now := time.Date(2026, 10, 4, 22, 30, 0, 0, time.UTC)
	w := newEventRiskFeedWorker(store, nil, func(context.Context, []string) ([]byte, []byte, error) {
		return []byte(`{"events":[{"source_id":"hl_meta","tier":"t0","asset":"ABC","event_type":"delist","venue_or_protocol":"hyperliquid","observed_at":"2019-01-01T00:00:00Z","title":"delist ABC","raw_json":{}}]}`), nil, nil
	})
	w.now = func() time.Time { return now }
	w.setConfig(&Config{EventRisk: &EventRiskConfig{Enabled: true, Sources: []string{"hl_meta"}}})
	w.tick(context.Background(), w.snapshotConfig())
	got, err := store.LoadRecentAltDataEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	if got[0].ObservedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("Go must overwrite Python observed_at, got %q", got[0].ObservedAt)
	}
	if got[0].State != eventRiskStateHold {
		t.Fatalf("T0 ingest must start in hold, got %q", got[0].State)
	}
}

func TestValidateEventRiskConfigs(t *testing.T) {
	errs := validateEventRiskConfig(&Config{EventRisk: &EventRiskConfig{
		Interval: "nope",
		Sources:  []string{"twitter"},
	}})
	if len(errs) != 2 {
		t.Fatalf("want interval+source errors, got %v", errs)
	}
	errs = validateEventRiskGateConfigs(&Config{Strategies: []StrategyConfig{{
		ID: "s1", Type: "perps",
		EventRiskGate: &EventRiskGateConfig{Enabled: true, OnFailure: "halt", EventTypes: []string{"rumor"}, MaxAge: "x"},
	}}})
	if len(errs) != 3 {
		t.Fatalf("want on_failure+type+max_age errors, got %v", errs)
	}
	if errs := validateEventRiskGateConfigs(&Config{}); len(errs) != 0 {
		t.Fatalf("absent block must be valid, got %v", errs)
	}
}

func TestEventRiskUnknownKeys(t *testing.T) {
	errs := validateEventRiskJSONKeys([]byte(`{"event_risk":{"enabled":false,"sentiment_score":1}}`))
	if len(errs) != 1 || !strings.Contains(errs[0], "sentiment_score") {
		t.Fatalf("unknown nested key must fail, got %v", errs)
	}
	errs = validateStrategyJSONKeys([]byte(`{"strategies":[{"id":"s1","event_risk_gate":{"enabled":false,"alpha":1}}]}`))
	if len(errs) != 1 || !strings.Contains(errs[0], "alpha") {
		t.Fatalf("unknown gate key must fail, got %v", errs)
	}
}

func TestEventRiskHotReloadBlockedWhileOpen(t *testing.T) {
	cur := &Config{Strategies: []StrategyConfig{{
		ID: "s1", Type: "perps", Script: "s.py", Platform: "hyperliquid",
		EventRiskGate: &EventRiskGateConfig{Enabled: false},
	}}}
	next := &Config{Strategies: []StrategyConfig{{
		ID: "s1", Type: "perps", Script: "s.py", Platform: "hyperliquid",
		EventRiskGate: &EventRiskGateConfig{Enabled: true},
	}}}
	state := &AppState{Strategies: map[string]*StrategyState{
		"s1": {ID: "s1", Positions: map[string]*Position{"BTC": {Symbol: "BTC", Quantity: 1}}},
	}}
	err := validateHotReloadStateCompatible(cur, next, state)
	if err == nil || !strings.Contains(err.Error(), "event_risk_gate") {
		t.Fatalf("open book must block gate toggle, got %v", err)
	}
	state.Strategies["s1"].Positions["BTC"].Quantity = 0
	if err := validateHotReloadStateCompatible(cur, next, state); err != nil {
		t.Fatalf("flat book must allow gate toggle: %v", err)
	}
}

func TestEventRiskStateRoundTrip(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC().Truncate(time.Nanosecond)
	state := &AppState{
		CycleCount: 1,
		Strategies: map[string]*StrategyState{
			"s1": {
				ID: "s1", Type: "perps", Platform: "hyperliquid",
				Cash: 1000, InitialCapital: 1000,
				EventRiskGate: EventRiskGateState{Active: true, RiskState: eventRiskStateWatch, Observed: true},
				Positions: map[string]*Position{
					"BTC": {Symbol: "BTC", Quantity: 1, AvgCost: 100, Side: "long", OpenedAt: now, EventRiskAtOpen: eventRiskStateWatch},
				},
				TradeHistory: []Trade{},
			},
		},
	}
	if err := db.SaveState(state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	loaded, err := db.LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	got := loaded.Strategies["s1"]
	if got.EventRiskGate.RiskState != eventRiskStateWatch || !got.EventRiskGate.Active {
		t.Fatalf("gate state %+v", got.EventRiskGate)
	}
	if got.Positions["BTC"].EventRiskAtOpen != eventRiskStateWatch {
		t.Fatalf("event_risk_at_open=%q", got.Positions["BTC"].EventRiskAtOpen)
	}
}

func TestStampEventRiskAtOpen(t *testing.T) {
	s := &StrategyState{
		EventRiskGate: EventRiskGateState{Active: true, RiskState: eventRiskStateWatch},
		Positions:     map[string]*Position{"BTC": {Symbol: "BTC", Quantity: 1}},
	}
	stampEventRiskAtOpenIfOpened(s, "BTC", true)
	if s.Positions["BTC"].EventRiskAtOpen != eventRiskStateWatch {
		t.Fatalf("stamp=%q", s.Positions["BTC"].EventRiskAtOpen)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
