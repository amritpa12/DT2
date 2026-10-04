package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (sdb *StateDB) InsertAltDataReading(row AltDataReading) (bool, error) {
	if sdb == nil || sdb.db == nil {
		return false, fmt.Errorf("state db unavailable")
	}
	if strings.TrimSpace(row.SourceID) == "" {
		return false, fmt.Errorf("alt_data_readings: source_id is required")
	}
	if strings.TrimSpace(row.ObservedAt) == "" {
		return false, fmt.Errorf("alt_data_readings: observed_at is required")
	}
	if strings.TrimSpace(row.Fingerprint) == "" {
		row.Fingerprint = altDataFingerprint(row)
	}
	res, err := sdb.db.Exec(
		`INSERT OR IGNORE INTO alt_data_readings
		   (source_id, asset, published_at, observed_at, url_hash, fingerprint, raw_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		row.SourceID, row.Asset, row.PublishedAt, row.ObservedAt, row.URLHash, row.Fingerprint, row.RawJSON)
	if err != nil {
		return false, fmt.Errorf("insert alt_data_readings: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert alt_data_readings rows: %w", err)
	}
	return n > 0, nil
}

func (sdb *StateDB) LoadRecentAltDataReadings(limit int) ([]AltDataReading, error) {
	if sdb == nil || sdb.db == nil {
		return nil, fmt.Errorf("state db unavailable")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := sdb.db.Query(
		`SELECT id, source_id, asset, published_at, observed_at, url_hash, fingerprint, raw_json
		   FROM alt_data_readings
		  ORDER BY observed_at DESC, id DESC
		  LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("load alt_data_readings: %w", err)
	}
	defer rows.Close()
	out := make([]AltDataReading, 0)
	for rows.Next() {
		var row AltDataReading
		if err := rows.Scan(&row.ID, &row.SourceID, &row.Asset, &row.PublishedAt, &row.ObservedAt, &row.URLHash, &row.Fingerprint, &row.RawJSON); err != nil {
			return nil, fmt.Errorf("scan alt_data_readings: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (sdb *StateDB) LatestAltDataObservedAt() (time.Time, bool, error) {
	if sdb == nil || sdb.db == nil {
		return time.Time{}, false, fmt.Errorf("state db unavailable")
	}
	var raw string
	err := sdb.db.QueryRow(`SELECT observed_at FROM alt_data_readings ORDER BY observed_at DESC, id DESC LIMIT 1`).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("latest alt_data_readings: %w", err)
	}
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		ts, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("latest alt_data_readings parse %q: %w", raw, err)
		}
	}
	return ts, true, nil
}

func (st *StateStore) InsertAltDataReading(row AltDataReading) (bool, error) {
	db, err := st.liveFile()
	if err != nil {
		return false, err
	}
	return db.InsertAltDataReading(row)
}

func (st *StateStore) LoadRecentAltDataReadings(limit int) ([]AltDataReading, error) {
	if st == nil {
		return nil, nil
	}
	db, err := st.liveFile()
	if err != nil {
		return nil, err
	}
	return db.LoadRecentAltDataReadings(limit)
}

func (st *StateStore) LatestAltDataObservedAt() (time.Time, bool, error) {
	if st == nil {
		return time.Time{}, false, nil
	}
	db, err := st.liveFile()
	if err != nil {
		return time.Time{}, false, err
	}
	return db.LatestAltDataObservedAt()
}
