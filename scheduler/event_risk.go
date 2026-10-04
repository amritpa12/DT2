package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	eventRiskDefaultInterval = 5 * time.Minute
	eventRiskDefaultMaxAge   = 24 * time.Hour
	eventRiskConfirmWindow   = 2 * time.Hour
	eventRiskSyndicateWindow = 6 * time.Hour
	eventRiskSyndicateMinJac = 0.8
	eventRiskDepegAbs        = 0.01

	eventRiskSourceHLMeta = "hl_meta"
	eventRiskSourcePeg    = "peg"

	eventRiskTierT0 = "t0"
	eventRiskTierT1 = "t1"
	eventRiskTierT2 = "t2"

	eventRiskStateWatch     = "watch"
	eventRiskStateHold      = "hold"
	eventRiskStateRetracted = "retracted"
	eventRiskStateExpired   = "expired"
	eventRiskStateClear     = "clear"

	eventRiskOnFailureOpen   = "open"
	eventRiskOnFailureClosed = "closed"

	eventRiskTypeDelist      = "delist"
	eventRiskTypeHalt        = "halt"
	eventRiskTypeExploit     = "exploit"
	eventRiskTypeDepeg       = "depeg"
	eventRiskTypeEnforcement = "enforcement"
	eventRiskTypeListing     = "listing"
	eventRiskTypeUnlock      = "unlock"
	eventRiskTypeUpgrade     = "upgrade"
)

var eventRiskDefaultSources = []string{eventRiskSourceHLMeta, eventRiskSourcePeg}

var eventRiskHoldTypes = []string{
	eventRiskTypeDelist, eventRiskTypeHalt, eventRiskTypeExploit,
	eventRiskTypeDepeg, eventRiskTypeEnforcement,
}

var eventRiskKnownTypes = []string{
	eventRiskTypeDelist, eventRiskTypeHalt, eventRiskTypeExploit,
	eventRiskTypeDepeg, eventRiskTypeEnforcement,
	eventRiskTypeListing, eventRiskTypeUnlock, eventRiskTypeUpgrade,
}

var eventRiskPaidPathFrags = []string{"/sponsored/", "/press-releases/", "/press-release/", "/partner-content/", "/studios/"}
var eventRiskPaidTitleFrags = []string{"press release", "sponsored", "partner content", "studios"}

// EventRiskConfig is the DEFAULT-OFF global poller. Enabled=false (or a
// missing block) starts no fetch work unless a strategy opts into the gate.
type EventRiskConfig struct {
	Enabled  bool     `json:"enabled"`
	Interval string   `json:"interval,omitempty"`
	Sources  []string `json:"sources,omitempty"`
}

// AltDataEvent is one confirmed or candidate event archived at our receipt time.
// ObservedAt is this process's clock. PublishedAt is the vendor stamp, if any.
type AltDataEvent struct {
	ID              int64  `json:"id,omitempty"`
	SourceID        string `json:"source_id"`
	Tier            string `json:"tier"`
	Asset           string `json:"asset"`
	EventType       string `json:"event_type"`
	VenueOrProtocol string `json:"venue_or_protocol,omitempty"`
	PublishedAt     string `json:"published_at,omitempty"`
	ObservedAt      string `json:"observed_at,omitempty"`
	URLHash         string `json:"url_hash,omitempty"`
	CanonicalKey    string `json:"canonical_key,omitempty"`
	State           string `json:"state,omitempty"`
	Title           string `json:"title,omitempty"`
	SyndicateOf     string `json:"syndicate_of,omitempty"`
	MarketFact      bool   `json:"market_fact,omitempty"`
	RawJSON         string `json:"raw_json,omitempty"`
}

type eventRiskFeedPayload struct {
	Events []eventRiskEventWire `json:"events"`
}

type eventRiskEventWire struct {
	SourceID        string          `json:"source_id"`
	Tier            string          `json:"tier"`
	Asset           string          `json:"asset"`
	EventType       string          `json:"event_type"`
	VenueOrProtocol string          `json:"venue_or_protocol"`
	PublishedAt     string          `json:"published_at"`
	ObservedAt      string          `json:"observed_at"`
	URLHash         string          `json:"url_hash"`
	URL             string          `json:"url"`
	Title           string          `json:"title"`
	SyndicateOf     string          `json:"syndicate_of"`
	MarketFact      bool            `json:"market_fact"`
	RawJSON         json.RawMessage `json:"raw_json"`
}

func cloneEventRiskConfig(c *EventRiskConfig) *EventRiskConfig {
	if c == nil {
		return nil
	}
	out := *c
	if c.Sources != nil {
		out.Sources = append([]string(nil), c.Sources...)
	}
	return &out
}

func formatEventRiskForLog(c *EventRiskConfig) string {
	if c == nil {
		return "(unset)"
	}
	if !c.Enabled {
		return "disabled"
	}
	return fmt.Sprintf("enabled interval=%q sources=%v", c.Interval, resolveEventRiskSources(c))
}

func eventRiskPollerEnabled(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	if cfg.EventRisk != nil && cfg.EventRisk.Enabled {
		return true
	}
	for _, sc := range cfg.Strategies {
		if eventRiskGateConfigured(sc) {
			return true
		}
	}
	return false
}

func resolveEventRiskSources(c *EventRiskConfig) []string {
	if c == nil || len(c.Sources) == 0 {
		return append([]string(nil), eventRiskDefaultSources...)
	}
	seen := make(map[string]bool, len(c.Sources))
	out := make([]string, 0, len(c.Sources))
	for _, raw := range c.Sources {
		src := normalizeEventRiskSource(raw)
		if src == "" || seen[src] {
			continue
		}
		seen[src] = true
		out = append(out, src)
	}
	sort.Strings(out)
	return out
}

func normalizeEventRiskSource(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func parseEventRiskSource(v string) (string, error) {
	switch src := normalizeEventRiskSource(v); src {
	case "", eventRiskSourceHLMeta, eventRiskSourcePeg:
		return src, nil
	default:
		return "", fmt.Errorf("event_risk.sources: unknown source %q (want %s)", v, strings.Join(eventRiskDefaultSources, ", "))
	}
}

func resolveEventRiskInterval(c *EventRiskConfig) time.Duration {
	if c == nil || strings.TrimSpace(c.Interval) == "" {
		return eventRiskDefaultInterval
	}
	d, err := time.ParseDuration(strings.TrimSpace(c.Interval))
	if err != nil || d <= 0 {
		return eventRiskDefaultInterval
	}
	return d
}

func validateEventRiskConfig(cfg *Config) []string {
	if cfg == nil || cfg.EventRisk == nil {
		return nil
	}
	var errs []string
	c := cfg.EventRisk
	if strings.TrimSpace(c.Interval) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(c.Interval))
		if err != nil {
			errs = append(errs, fmt.Sprintf("event_risk.interval: invalid duration %q: %v", c.Interval, err))
		} else if d <= 0 {
			errs = append(errs, fmt.Sprintf("event_risk.interval must be > 0, got %s", c.Interval))
		}
	}
	for _, src := range c.Sources {
		if _, err := parseEventRiskSource(src); err != nil {
			errs = append(errs, err.Error())
		}
	}
	sort.Strings(errs)
	return errs
}

func normalizeEventRiskTier(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func normalizeEventRiskType(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func parseEventRiskType(v string) (string, error) {
	t := normalizeEventRiskType(v)
	for _, known := range eventRiskKnownTypes {
		if t == known {
			return t, nil
		}
	}
	if t == "" {
		return "", nil
	}
	return "", fmt.Errorf("unknown event_type %q (want %s)", v, strings.Join(eventRiskKnownTypes, ", "))
}

func eventTypeCanHold(t string) bool {
	t = normalizeEventRiskType(t)
	for _, known := range eventRiskHoldTypes {
		if t == known {
			return true
		}
	}
	return false
}

func eventTypeVenueBound(t string) bool {
	switch normalizeEventRiskType(t) {
	case eventRiskTypeHalt, eventRiskTypeDelist:
		return true
	}
	return false
}

func eventRiskSourceOwner(sourceID string) string {
	src := normalizeEventRiskSource(sourceID)
	if i := strings.IndexByte(src, ':'); i > 0 {
		return src[:i]
	}
	if strings.HasSuffix(src, "-syndicated") {
		return strings.TrimSuffix(src, "-syndicated")
	}
	return src
}

func normalizeEventRiskURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return strings.ToLower(raw)
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	host = strings.TrimPrefix(host, "mobile.")
	host = strings.TrimSuffix(host, ".cdn.ampproject.org")
	if strings.HasSuffix(host, ".amp") {
		host = strings.TrimSuffix(host, ".amp")
	}
	q := u.Query()
	for _, k := range []string{"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "amp"} {
		q.Del(k)
	}
	u.Host = host
	u.Scheme = strings.ToLower(u.Scheme)
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String()
}

func eventRiskURLHash(raw string) string {
	norm := normalizeEventRiskURL(raw)
	if norm == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:16])
}

func paidContentDropped(url, title string) bool {
	u := strings.ToLower(url)
	for _, frag := range eventRiskPaidPathFrags {
		if strings.Contains(u, frag) {
			return true
		}
	}
	t := strings.ToLower(title)
	for _, frag := range eventRiskPaidTitleFrags {
		if strings.Contains(t, frag) {
			return true
		}
	}
	return false
}

func eventRiskCanonicalKey(asset, eventType, venue string, observed time.Time) string {
	bucket := observed.UTC().Truncate(time.Hour).Format(time.RFC3339)
	return strings.Join([]string{
		strings.ToUpper(strings.TrimSpace(asset)),
		normalizeEventRiskType(eventType),
		strings.ToLower(strings.TrimSpace(venue)),
		bucket,
	}, "|")
}

func stampEventsObservedAt(rows []AltDataEvent, now time.Time) []AltDataEvent {
	if len(rows) == 0 {
		return rows
	}
	stamp := now.UTC()
	stampStr := stamp.Format(time.RFC3339Nano)
	out := make([]AltDataEvent, 0, len(rows))
	for _, row := range rows {
		row.SourceID = normalizeEventRiskSource(row.SourceID)
		row.Tier = normalizeEventRiskTier(row.Tier)
		row.Asset = strings.ToUpper(strings.TrimSpace(row.Asset))
		row.EventType = normalizeEventRiskType(row.EventType)
		row.VenueOrProtocol = strings.ToLower(strings.TrimSpace(row.VenueOrProtocol))
		row.PublishedAt = strings.TrimSpace(row.PublishedAt)
		row.Title = strings.TrimSpace(row.Title)
		row.SyndicateOf = normalizeEventRiskSource(row.SyndicateOf)
		row.RawJSON = strings.TrimSpace(row.RawJSON)
		row.ObservedAt = stampStr
		if strings.TrimSpace(row.URLHash) == "" {
			// leave empty; fingerprint is canonical_key + source
		} else {
			row.URLHash = strings.TrimSpace(row.URLHash)
		}
		row.CanonicalKey = eventRiskCanonicalKey(row.Asset, row.EventType, row.VenueOrProtocol, stamp)
		if row.State == "" {
			row.State = eventRiskStateWatch
		}
		out = append(out, row)
	}
	return out
}

func parseEventRiskFeedPayload(raw []byte) ([]AltDataEvent, error) {
	raw = bytesTrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("event_risk_feed: empty stdout")
	}
	var payload eventRiskFeedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("event_risk_feed: parse stdout: %w", err)
	}
	out := make([]AltDataEvent, 0, len(payload.Events))
	for _, row := range payload.Events {
		rawJSON := strings.TrimSpace(string(row.RawJSON))
		if len(rawJSON) >= 2 && rawJSON[0] == '"' {
			var asString string
			if err := json.Unmarshal(row.RawJSON, &asString); err == nil {
				rawJSON = asString
			}
		}
		urlHash := strings.TrimSpace(row.URLHash)
		if urlHash == "" && strings.TrimSpace(row.URL) != "" {
			urlHash = eventRiskURLHash(row.URL)
		}
		if paidContentDropped(row.URL, row.Title) {
			fmt.Printf("[event-risk] dropped paid/sponsored item source=%s url=%s\n", row.SourceID, row.URL)
			continue
		}
		out = append(out, AltDataEvent{
			SourceID:        row.SourceID,
			Tier:            row.Tier,
			Asset:           row.Asset,
			EventType:       row.EventType,
			VenueOrProtocol: row.VenueOrProtocol,
			PublishedAt:     row.PublishedAt,
			ObservedAt:      row.ObservedAt,
			URLHash:         urlHash,
			Title:           row.Title,
			SyndicateOf:     row.SyndicateOf,
			MarketFact:      row.MarketFact,
			RawJSON:         rawJSON,
		})
	}
	return out, nil
}

func parseEventRiskTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts, true
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts, true
	}
	return time.Time{}, false
}

func tokenizeEventRiskTitle(s string) []string {
	var b strings.Builder
	seen := make(map[string]bool)
	var out []string
	flush := func() {
		tok := strings.ToLower(strings.TrimSpace(b.String()))
		b.Reset()
		if len(tok) < 2 || seen[tok] {
			return
		}
		seen[tok] = true
		out = append(out, tok)
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func jaccardTokens(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]bool, len(a))
	for _, t := range a {
		set[t] = true
	}
	inter := 0
	for _, t := range b {
		if set[t] {
			inter++
		}
	}
	union := len(set)
	for _, t := range b {
		if !set[t] {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func eventsWithin(a, b AltDataEvent, window time.Duration) bool {
	ta, oka := parseEventRiskTime(a.ObservedAt)
	tb, okb := parseEventRiskTime(b.ObservedAt)
	if !oka || !okb {
		return false
	}
	d := ta.Sub(tb)
	if d < 0 {
		d = -d
	}
	return d <= window
}

func eventsAreSyndicated(a, b AltDataEvent) bool {
	if a.SourceID == "" || b.SourceID == "" {
		return false
	}
	if a.SourceID == b.SourceID {
		return false
	}
	if a.SyndicateOf != "" && (a.SyndicateOf == b.SourceID || a.SyndicateOf == eventRiskSourceOwner(b.SourceID)) {
		return true
	}
	if b.SyndicateOf != "" && (b.SyndicateOf == a.SourceID || b.SyndicateOf == eventRiskSourceOwner(a.SourceID)) {
		return true
	}
	if eventRiskSourceOwner(a.SourceID) == eventRiskSourceOwner(b.SourceID) {
		return true
	}
	if a.URLHash != "" && a.URLHash == b.URLHash {
		return true
	}
	if a.Asset == b.Asset && a.EventType == b.EventType && eventsWithin(a, b, eventRiskSyndicateWindow) {
		if jaccardTokens(tokenizeEventRiskTitle(a.Title), tokenizeEventRiskTitle(b.Title)) >= eventRiskSyndicateMinJac {
			return true
		}
	}
	return false
}

func markSyndicatedCopies(rows []AltDataEvent) []AltDataEvent {
	out := append([]AltDataEvent(nil), rows...)
	for i := range out {
		for j := 0; j < i; j++ {
			if eventsAreSyndicated(out[i], out[j]) && out[i].SyndicateOf == "" {
				out[i].SyndicateOf = out[j].SourceID
			}
		}
	}
	return out
}

func eventExpired(ev AltDataEvent, now time.Time, maxAge time.Duration) bool {
	if ev.State == eventRiskStateRetracted || ev.State == eventRiskStateExpired {
		return true
	}
	if eventTypeVenueBound(ev.EventType) {
		return false
	}
	ts, ok := parseEventRiskTime(ev.ObservedAt)
	if !ok {
		return true
	}
	if maxAge <= 0 {
		maxAge = eventRiskDefaultMaxAge
	}
	return now.Sub(ts) > maxAge
}

type eventRiskEval struct {
	State  string
	Holds  bool
	Detail string
	Key    string
}

func evaluateEventRisk(events []AltDataEvent, asset string, allowedTypes []string, now time.Time, maxAge time.Duration) eventRiskEval {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	allowed := make(map[string]bool, len(allowedTypes))
	if len(allowedTypes) == 0 {
		for _, t := range eventRiskHoldTypes {
			allowed[t] = true
		}
	} else {
		for _, t := range allowedTypes {
			allowed[normalizeEventRiskType(t)] = true
		}
	}
	var live []AltDataEvent
	for _, ev := range events {
		if ev.Asset != "" && ev.Asset != asset && ev.Asset != "*" {
			continue
		}
		if !allowed[ev.EventType] || !eventTypeCanHold(ev.EventType) {
			continue
		}
		if eventExpired(ev, now, maxAge) {
			continue
		}
		if ev.State == eventRiskStateRetracted || ev.State == eventRiskStateExpired {
			continue
		}
		if ev.Tier == eventRiskTierT2 {
			continue
		}
		live = append(live, ev)
	}
	if len(live) == 0 {
		return eventRiskEval{State: eventRiskStateClear, Detail: "no confirmed event"}
	}

	var t0 []AltDataEvent
	var t1 []AltDataEvent
	for _, ev := range live {
		switch ev.Tier {
		case eventRiskTierT0:
			t0 = append(t0, ev)
		case eventRiskTierT1:
			t1 = append(t1, ev)
		}
	}
	if len(t0) > 0 {
		ev := t0[0]
		return eventRiskEval{
			State:  eventRiskStateHold,
			Holds:  true,
			Detail: fmt.Sprintf("T0 %s %s %s", ev.EventType, ev.Asset, ev.SourceID),
			Key:    ev.CanonicalKey,
		}
	}

	independents := independentT1(t1)
	if len(independents) >= 2 && t1WithinConfirmWindow(independents) {
		return eventRiskEval{
			State:  eventRiskStateHold,
			Holds:  true,
			Detail: fmt.Sprintf("T1 confirmed %s %s (%d sources)", independents[0].EventType, independents[0].Asset, len(independents)),
			Key:    independents[0].CanonicalKey,
		}
	}
	for _, ev := range independents {
		if ev.MarketFact {
			return eventRiskEval{
				State:  eventRiskStateHold,
				Holds:  true,
				Detail: fmt.Sprintf("T1+market %s %s %s", ev.EventType, ev.Asset, ev.SourceID),
				Key:    ev.CanonicalKey,
			}
		}
	}
	if len(independents) == 1 {
		ev := independents[0]
		return eventRiskEval{
			State:  eventRiskStateWatch,
			Detail: fmt.Sprintf("T1 unconfirmed %s %s %s", ev.EventType, ev.Asset, ev.SourceID),
			Key:    ev.CanonicalKey,
		}
	}
	if len(t1) > 0 {
		return eventRiskEval{State: eventRiskStateWatch, Detail: "T1 copies collapsed as syndication"}
	}
	return eventRiskEval{State: eventRiskStateClear, Detail: "no confirmed event"}
}

func independentT1(rows []AltDataEvent) []AltDataEvent {
	var out []AltDataEvent
	for _, ev := range rows {
		if ev.SyndicateOf != "" {
			continue
		}
		dup := false
		for _, seen := range out {
			if eventsAreSyndicated(ev, seen) || eventRiskSourceOwner(ev.SourceID) == eventRiskSourceOwner(seen.SourceID) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, ev)
		}
	}
	return out
}

func t1WithinConfirmWindow(rows []AltDataEvent) bool {
	if len(rows) < 2 {
		return false
	}
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if rows[i].Asset == rows[j].Asset && rows[i].EventType == rows[j].EventType && eventsWithin(rows[i], rows[j], eventRiskConfirmWindow) {
				return true
			}
		}
	}
	return false
}
