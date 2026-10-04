package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStampReadingsObservedAtOverwritesVendorClock(t *testing.T) {
	now := time.Date(2026, 10, 4, 22, 15, 30, 123456789, time.UTC)
	rows := stampReadingsObservedAt([]AltDataReading{{
		SourceID:    "FNG",
		Asset:       "btc",
		PublishedAt: "2026-10-04T00:00:00Z",
		ObservedAt:  "2018-01-01T00:00:00Z",
		RawJSON:     `{"value":50}`,
	}}, now)
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].ObservedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("observed_at must be our receipt clock, got %q", rows[0].ObservedAt)
	}
	if rows[0].PublishedAt != "2026-10-04T00:00:00Z" {
		t.Fatalf("published_at must keep the vendor stamp, got %q", rows[0].PublishedAt)
	}
	if rows[0].SourceID != "fng" || rows[0].Asset != "BTC" {
		t.Fatalf("source/asset normalize: %+v", rows[0])
	}
	if rows[0].Fingerprint == "" {
		t.Fatal("fingerprint must be filled when the vendor omits it")
	}
}

func TestAltDataReadingDedupUsesFingerprint(t *testing.T) {
	store := openTestStore(t, openTestDB(t))
	now := time.Date(2026, 10, 4, 22, 0, 0, 0, time.UTC)
	row := stampReadingsObservedAt([]AltDataReading{{
		SourceID:    "fng",
		PublishedAt: "2026-10-04T00:00:00Z",
		RawJSON:     `{"value":12}`,
	}}, now)[0]
	first, err := store.InsertAltDataReading(row)
	if err != nil || !first {
		t.Fatalf("first insert: inserted=%v err=%v", first, err)
	}
	later := row
	later.ObservedAt = now.Add(time.Hour).Format(time.RFC3339Nano)
	later.RawJSON = `{"value":12,"extra":"revised"}`
	second, err := store.InsertAltDataReading(later)
	if err != nil {
		t.Fatal(err)
	}
	if second {
		t.Fatal("same source/asset/fingerprint must not insert a second row — revisions keep the first receipt")
	}
	got, err := store.LoadRecentAltDataReadings(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 archived row, got %d", len(got))
	}
	if got[0].ObservedAt != row.ObservedAt {
		t.Fatalf("first receipt time must stand, got %q want %q", got[0].ObservedAt, row.ObservedAt)
	}
}

func TestAltDataRecorderDisabledIsNoOp(t *testing.T) {
	store := openTestStore(t, openTestDB(t))
	fetched := 0
	w := newAltDataRecorderWorker(store, nil, func(context.Context, []string) ([]byte, []byte, error) {
		fetched++
		return []byte(`{"readings":[{"source_id":"fng","raw_json":"{}"}]}`), nil, nil
	})
	w.setConfig(&AltDataRecorderConfig{Enabled: false})
	w.tick(context.Background(), w.snapshotConfig())
	if fetched != 0 {
		t.Fatalf("disabled recorder must not fetch, got %d fetches", fetched)
	}
	got, err := store.LoadRecentAltDataReadings(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("disabled recorder must write nothing, got %d", len(got))
	}
}

func TestAltDataRecorderTickStampsObservedAtBeforeWrite(t *testing.T) {
	store := openTestStore(t, openTestDB(t))
	now := time.Date(2026, 10, 4, 22, 30, 0, 0, time.UTC)
	w := newAltDataRecorderWorker(store, nil, func(context.Context, []string) ([]byte, []byte, error) {
		return []byte(`{"readings":[{"source_id":"fng","published_at":"2026-10-04T00:00:00Z","observed_at":"2019-01-01T00:00:00Z","raw_json":{"value":40}}]}`), nil, nil
	})
	w.now = func() time.Time { return now }
	w.setConfig(&AltDataRecorderConfig{Enabled: true, Sources: []string{"fng"}})
	w.tick(context.Background(), w.snapshotConfig())
	got, err := store.LoadRecentAltDataReadings(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d", len(got))
	}
	if got[0].ObservedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("Go must overwrite Python observed_at, got %q", got[0].ObservedAt)
	}
	if got[0].PublishedAt != "2026-10-04T00:00:00Z" {
		t.Fatalf("published_at=%q", got[0].PublishedAt)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(got[0].RawJSON), &raw); err != nil {
		t.Fatalf("raw_json: %v", err)
	}
}

func TestAltDataRecorderUnknownKeys(t *testing.T) {
	errs := validateAltDataJSONKeys([]byte(`{"alt_data_recorder":{"enabled":false,"sentiment_score":1}}`))
	if len(errs) != 1 || !strings.Contains(errs[0], "sentiment_score") {
		t.Fatalf("unknown nested key must fail, got %v", errs)
	}
}

func TestValidateAltDataRecorderConfig(t *testing.T) {
	errs := validateAltDataRecorderConfig(&Config{AltDataRecorder: &AltDataRecorderConfig{
		Enabled:  true,
		Interval: "not-a-duration",
		Sources:  []string{"twitter"},
	}})
	if len(errs) != 2 {
		t.Fatalf("want interval+source errors, got %v", errs)
	}
	if !strings.Contains(strings.Join(errs, "\n"), "twitter") {
		t.Fatalf("unknown source must be named, got %v", errs)
	}
	if errs := validateAltDataRecorderConfig(&Config{}); len(errs) != 0 {
		t.Fatalf("absent block must be valid (DEFAULT-OFF), got %v", errs)
	}
}

func TestParseAltDataRecorderPayloadAcceptsObjectRawJSON(t *testing.T) {
	rows, err := parseAltDataRecorderPayload([]byte(`{"readings":[{"source_id":"peg","asset":"USDT","raw_json":{"px":1.0}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SourceID != "peg" {
		t.Fatalf("%+v", rows)
	}
}
