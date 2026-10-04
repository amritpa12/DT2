package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (sdb *StateDB) InsertAltDataEvent(row AltDataEvent) (bool, error) {
	if sdb == nil || sdb.db == nil {
		return false, fmt.Errorf("state db unavailable")
	}
	if strings.TrimSpace(row.SourceID) == "" {
		return false, fmt.Errorf("alt_data_events: source_id is required")
	}
	if strings.TrimSpace(row.ObservedAt) == "" {
		return false, fmt.Errorf("alt_data_events: observed_at is required")
	}
	if strings.TrimSpace(row.CanonicalKey) == "" {
		return false, fmt.Errorf("alt_data_events: canonical_key is required")
	}
	if strings.TrimSpace(row.State) == "" {
		row.State = eventRiskStateWatch
	}
	res, err := sdb.db.Exec(
		`INSERT OR IGNORE INTO alt_data_events
		   (source_id, tier, asset, event_type, venue_or_protocol, published_at, observed_at,
		    url_hash, canonical_key, state, title, syndicate_of, raw_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.SourceID, row.Tier, row.Asset, row.EventType, row.VenueOrProtocol,
		row.PublishedAt, row.ObservedAt, row.URLHash, row.CanonicalKey, row.State,
		row.Title, row.SyndicateOf, row.RawJSON)
	if err != nil {
		return false, fmt.Errorf("insert alt_data_events: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert alt_data_events rows: %w", err)
	}
	return n > 0, nil
}

func (sdb *StateDB) UpdateAltDataEventState(canonicalKey, sourceID, state string) error {
	if sdb == nil || sdb.db == nil {
		return fmt.Errorf("state db unavailable")
	}
	if strings.TrimSpace(canonicalKey) == "" || strings.TrimSpace(sourceID) == "" {
		return fmt.Errorf("alt_data_events: canonical_key and source_id are required")
	}
	_, err := sdb.db.Exec(
		`UPDATE alt_data_events SET state = ? WHERE canonical_key = ? AND source_id = ?`,
		state, canonicalKey, sourceID)
	if err != nil {
		return fmt.Errorf("update alt_data_events state: %w", err)
	}
	return nil
}

func (sdb *StateDB) RetractAltDataEventsMissing(sourceID, eventType string, stillActive map[string]bool) (int, error) {
	if sdb == nil || sdb.db == nil {
		return 0, fmt.Errorf("state db unavailable")
	}
	rows, err := sdb.db.Query(
		`SELECT canonical_key, source_id, asset FROM alt_data_events
		  WHERE source_id = ? AND event_type = ? AND state IN (?, ?)`,
		sourceID, eventType, eventRiskStateHold, eventRiskStateWatch)
	if err != nil {
		return 0, fmt.Errorf("list alt_data_events for retract: %w", err)
	}
	defer rows.Close()
	type key struct{ canon, source, asset string }
	var stale []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.canon, &k.source, &k.asset); err != nil {
			return 0, fmt.Errorf("scan alt_data_events retract: %w", err)
		}
		if stillActive[k.asset] {
			continue
		}
		stale = append(stale, k)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, k := range stale {
		if err := sdb.UpdateAltDataEventState(k.canon, k.source, eventRiskStateRetracted); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func scanAltDataEvent(scanner interface {
	Scan(dest ...any) error
}) (AltDataEvent, error) {
	var row AltDataEvent
	err := scanner.Scan(
		&row.ID, &row.SourceID, &row.Tier, &row.Asset, &row.EventType, &row.VenueOrProtocol,
		&row.PublishedAt, &row.ObservedAt, &row.URLHash, &row.CanonicalKey, &row.State,
		&row.Title, &row.SyndicateOf, &row.RawJSON)
	return row, err
}

func (sdb *StateDB) LoadRecentAltDataEvents(limit int) ([]AltDataEvent, error) {
	if sdb == nil || sdb.db == nil {
		return nil, fmt.Errorf("state db unavailable")
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := sdb.db.Query(
		`SELECT id, source_id, tier, asset, event_type, venue_or_protocol, published_at, observed_at,
		        url_hash, canonical_key, state, title, syndicate_of, raw_json
		   FROM alt_data_events
		  ORDER BY observed_at DESC, id DESC
		  LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("load alt_data_events: %w", err)
	}
	defer rows.Close()
	out := make([]AltDataEvent, 0)
	for rows.Next() {
		row, err := scanAltDataEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan alt_data_events: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (sdb *StateDB) LatestAltDataEventObservedAt() (time.Time, bool, error) {
	if sdb == nil || sdb.db == nil {
		return time.Time{}, false, fmt.Errorf("state db unavailable")
	}
	var raw string
	err := sdb.db.QueryRow(`SELECT observed_at FROM alt_data_events ORDER BY observed_at DESC, id DESC LIMIT 1`).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("latest alt_data_events: %w", err)
	}
	ts, ok := parseEventRiskTime(raw)
	if !ok {
		return time.Time{}, false, fmt.Errorf("latest alt_data_events parse %q", raw)
	}
	return ts, true, nil
}

func (st *StateStore) InsertAltDataEvent(row AltDataEvent) (bool, error) {
	db, err := st.liveFile()
	if err != nil {
		return false, err
	}
	return db.InsertAltDataEvent(row)
}

func (st *StateStore) UpdateAltDataEventState(canonicalKey, sourceID, state string) error {
	db, err := st.liveFile()
	if err != nil {
		return err
	}
	return db.UpdateAltDataEventState(canonicalKey, sourceID, state)
}

func (st *StateStore) RetractAltDataEventsMissing(sourceID, eventType string, stillActive map[string]bool) (int, error) {
	db, err := st.liveFile()
	if err != nil {
		return 0, err
	}
	return db.RetractAltDataEventsMissing(sourceID, eventType, stillActive)
}

func (st *StateStore) LoadRecentAltDataEvents(limit int) ([]AltDataEvent, error) {
	if st == nil {
		return nil, fmt.Errorf("state store unavailable")
	}
	db, err := st.liveFile()
	if err != nil {
		return nil, err
	}
	return db.LoadRecentAltDataEvents(limit)
}

func (st *StateStore) LatestAltDataEventObservedAt() (time.Time, bool, error) {
	if st == nil {
		return time.Time{}, false, nil
	}
	db, err := st.liveFile()
	if err != nil {
		return time.Time{}, false, err
	}
	return db.LatestAltDataEventObservedAt()
}
