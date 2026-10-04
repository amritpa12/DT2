package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	altDataRecorderScript      = "shared_tools/alt_data_recorder.py"
	altDataDefaultInterval     = 5 * time.Minute
	altDataDefaultSourceFNG    = "fng"
	altDataDefaultSourceHLMeta = "hl_meta"
	altDataDefaultSourcePeg    = "peg"
)

var altDataDefaultSources = []string{altDataDefaultSourceFNG, altDataDefaultSourceHLMeta, altDataDefaultSourcePeg}

// AltDataRecorderConfig is the DEFAULT-OFF point-in-time archive. Enabled=false
// (or a missing block) changes no trading behaviour and starts no poller work.
type AltDataRecorderConfig struct {
	Enabled  bool     `json:"enabled"`
	Interval string   `json:"interval,omitempty"`
	Sources  []string `json:"sources,omitempty"`
}

// AltDataReading is one raw vendor payload archived at our receipt time.
// ObservedAt is this process's clock. PublishedAt is the vendor stamp, if any,
// and is never used as the as-of time.
type AltDataReading struct {
	ID          int64  `json:"id,omitempty"`
	SourceID    string `json:"source_id"`
	Asset       string `json:"asset,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	ObservedAt  string `json:"observed_at,omitempty"`
	URLHash     string `json:"url_hash,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	RawJSON     string `json:"raw_json,omitempty"`
}

type altDataRecorderPayload struct {
	Readings []altDataReadingWire `json:"readings"`
}

type altDataReadingWire struct {
	SourceID    string          `json:"source_id"`
	Asset       string          `json:"asset"`
	PublishedAt string          `json:"published_at"`
	ObservedAt  string          `json:"observed_at"`
	URLHash     string          `json:"url_hash"`
	Fingerprint string          `json:"fingerprint"`
	RawJSON     json.RawMessage `json:"raw_json"`
}

func altDataRecorderEnabled(cfg *Config) bool {
	return cfg != nil && cfg.AltDataRecorder != nil && cfg.AltDataRecorder.Enabled
}

func cloneAltDataRecorderConfig(c *AltDataRecorderConfig) *AltDataRecorderConfig {
	if c == nil {
		return nil
	}
	out := *c
	if c.Sources != nil {
		out.Sources = append([]string(nil), c.Sources...)
	}
	return &out
}

func formatAltDataRecorderForLog(c *AltDataRecorderConfig) string {
	if c == nil {
		return "(unset)"
	}
	if !c.Enabled {
		return "disabled"
	}
	return fmt.Sprintf("enabled interval=%q sources=%v", c.Interval, resolveAltDataSources(c))
}

func resolveAltDataSources(c *AltDataRecorderConfig) []string {
	if c == nil || len(c.Sources) == 0 {
		return append([]string(nil), altDataDefaultSources...)
	}
	seen := make(map[string]bool, len(c.Sources))
	out := make([]string, 0, len(c.Sources))
	for _, raw := range c.Sources {
		src := normalizeAltDataSource(raw)
		if src == "" || seen[src] {
			continue
		}
		seen[src] = true
		out = append(out, src)
	}
	sort.Strings(out)
	return out
}

func normalizeAltDataSource(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func parseAltDataSource(v string) (string, error) {
	switch src := normalizeAltDataSource(v); src {
	case "", altDataDefaultSourceFNG, altDataDefaultSourceHLMeta, altDataDefaultSourcePeg:
		return src, nil
	default:
		return "", fmt.Errorf("alt_data_recorder.sources: unknown source %q (want %s)", v, strings.Join(altDataDefaultSources, ", "))
	}
}

func resolveAltDataRecorderInterval(c *AltDataRecorderConfig) time.Duration {
	if c == nil || strings.TrimSpace(c.Interval) == "" {
		return altDataDefaultInterval
	}
	d, err := time.ParseDuration(strings.TrimSpace(c.Interval))
	if err != nil || d <= 0 {
		return altDataDefaultInterval
	}
	return d
}

func validateAltDataRecorderConfig(cfg *Config) []string {
	if cfg == nil || cfg.AltDataRecorder == nil {
		return nil
	}
	var errs []string
	c := cfg.AltDataRecorder
	if strings.TrimSpace(c.Interval) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(c.Interval))
		if err != nil {
			errs = append(errs, fmt.Sprintf("alt_data_recorder.interval: invalid duration %q: %v", c.Interval, err))
		} else if d <= 0 {
			errs = append(errs, fmt.Sprintf("alt_data_recorder.interval must be > 0, got %s", c.Interval))
		}
	}
	for _, src := range c.Sources {
		if _, err := parseAltDataSource(src); err != nil {
			errs = append(errs, err.Error())
		}
	}
	sort.Strings(errs)
	return errs
}

// stampReadingsObservedAt overwrites ObservedAt with our clock and fills a
// missing fingerprint. Vendor PublishedAt is left untouched.
func stampReadingsObservedAt(rows []AltDataReading, now time.Time) []AltDataReading {
	if len(rows) == 0 {
		return rows
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	out := make([]AltDataReading, len(rows))
	for i, row := range rows {
		row.SourceID = normalizeAltDataSource(row.SourceID)
		row.Asset = strings.ToUpper(strings.TrimSpace(row.Asset))
		row.PublishedAt = strings.TrimSpace(row.PublishedAt)
		row.URLHash = strings.TrimSpace(row.URLHash)
		row.RawJSON = strings.TrimSpace(row.RawJSON)
		row.ObservedAt = stamp
		if strings.TrimSpace(row.Fingerprint) == "" {
			row.Fingerprint = altDataFingerprint(row)
		} else {
			row.Fingerprint = strings.TrimSpace(row.Fingerprint)
		}
		out[i] = row
	}
	return out
}

func altDataFingerprint(row AltDataReading) string {
	if row.URLHash != "" {
		return row.URLHash
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{row.SourceID, row.Asset, row.PublishedAt, row.RawJSON}, "|")))
	return hex.EncodeToString(sum[:16])
}

func parseAltDataRecorderPayload(raw []byte) ([]AltDataReading, error) {
	raw = bytesTrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("alt_data_recorder: empty stdout")
	}
	var payload altDataRecorderPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("alt_data_recorder: parse stdout: %w", err)
	}
	out := make([]AltDataReading, 0, len(payload.Readings))
	for _, row := range payload.Readings {
		rawJSON := strings.TrimSpace(string(row.RawJSON))
		if len(rawJSON) >= 2 && rawJSON[0] == '"' {
			var asString string
			if err := json.Unmarshal(row.RawJSON, &asString); err == nil {
				rawJSON = asString
			}
		}
		out = append(out, AltDataReading{
			SourceID:    row.SourceID,
			Asset:       row.Asset,
			PublishedAt: row.PublishedAt,
			ObservedAt:  row.ObservedAt,
			URLHash:     row.URLHash,
			Fingerprint: row.Fingerprint,
			RawJSON:     rawJSON,
		})
	}
	return out, nil
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
