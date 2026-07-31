package activity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func TestMissingActivityFileReturnsZeroStatistics(t *testing.T) {
	identity := SkillIdentity{
		SkillKey:  "sha256:skill-key",
		SkillID:   "redis",
		SkillName: "Redis",
		SkillHash: "sha256:current",
	}
	stats, err := statsForIdentity(filepath.Join(t.TempDir(), "missing.jsonl"), identity, DefaultFeedbackLimit)
	if err != nil {
		t.Fatal(err)
	}
	if stats.SkillKey != identity.SkillKey || stats.SkillID != identity.SkillID || stats.CurrentSkillHash != identity.SkillHash {
		t.Fatalf("identity metadata = %#v", stats)
	}
	if stats.RetrievalCount != 0 || stats.UseCount != 0 || stats.FeedbackCount != 0 {
		t.Fatalf("non-zero counts = %#v", stats)
	}
	if stats.FirstRetrievedAt != nil || stats.LastRetrievedAt != nil || stats.FirstUsedAt != nil || stats.LastUsedAt != nil || stats.LastFeedbackAt != nil {
		t.Fatalf("non-zero timestamps = %#v", stats)
	}
	if len(stats.RecentFeedback) != 0 || len(stats.Warnings) != 0 {
		t.Fatalf("unexpected feedback or warnings = %#v", stats)
	}
}

func TestActivityLockTimeoutBehavior(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	held := flock.New(path + ".lock")
	if err := held.Lock(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Unlock() })

	event := validEvent(EventUse, "evt_timeout", time.Now().UTC())
	started := time.Now()
	_, err := appendEventWithTimeout(path, event, 30*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out acquiring activity lock") || !strings.Contains(err.Error(), path) {
		t.Fatalf("timeout error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("lock timeout took %s", elapsed)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("activity file exists after timeout: %v", statErr)
	}
}

func TestConcurrentActivityWritersPreserveEveryEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	const writers = 32
	start := make(chan struct{})
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for index := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			event := validEvent(EventRetrieval, fmt.Sprintf("evt_%03d", index), time.Date(2026, 7, 31, 18, 0, index, 0, time.UTC))
			_, err := appendEvent(path, event)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	scan, err := scanEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Warnings) != 0 || len(scan.Events) != writers {
		t.Fatalf("events=%d warnings=%#v", len(scan.Events), scan.Warnings)
	}
	seen := map[string]bool{}
	for _, event := range scan.Events {
		if seen[event.EventID] {
			t.Fatalf("duplicate event id %s", event.EventID)
		}
		seen[event.EventID] = true
	}
}

func TestMalformedAndInterruptedActivityLinesReturnWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	event := validEvent(EventRetrieval, "evt_valid", time.Date(2026, 7, 31, 18, 0, 0, 0, time.UTC))
	valid, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	unknown := event
	unknown.EventID = "evt_unknown"
	unknown.EventType = EventType("future-event")
	unknownData, err := json.Marshal(unknown)
	if err != nil {
		t.Fatal(err)
	}
	var content bytes.Buffer
	content.WriteString("\n")
	content.Write(valid)
	content.WriteByte('\n')
	content.WriteString("not-json\n")
	content.Write(unknownData)
	content.WriteByte('\n')
	content.WriteString(`{"schema_version":1`)
	if err := os.WriteFile(path, content.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	scan, err := scanEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Events) != 1 || scan.Events[0].EventID != event.EventID {
		t.Fatalf("events = %#v", scan.Events)
	}
	if len(scan.Warnings) != 3 {
		t.Fatalf("warnings = %#v", scan.Warnings)
	}
	joined := strings.Join(scan.Warnings, "\n")
	for _, want := range []string{path, "malformed", "unknown event_type", "interrupted final line"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warnings %q do not contain %q", joined, want)
		}
	}
}

func TestActivityTextAndEventSizeValidation(t *testing.T) {
	base := validEvent(EventFeedback, "evt_feedback", time.Date(2026, 7, 31, 18, 0, 0, 0, time.UTC))
	base.Sentiment = SentimentPositive
	base.Situation = "A concrete situation"
	base.Details = "A concrete successful outcome"
	base.ProposedChange = "Preserve the effective instruction"
	if err := ValidateEvent(base); err != nil {
		t.Fatalf("valid feedback rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*Event)
		want string
	}{
		{name: "sentiment", edit: func(event *Event) { event.Sentiment = "neutral" }, want: "sentiment"},
		{name: "situation missing", edit: func(event *Event) { event.Situation = " \t" }, want: "situation"},
		{name: "details missing", edit: func(event *Event) { event.Details = "\n" }, want: "details"},
		{name: "proposed change missing", edit: func(event *Event) { event.ProposedChange = " " }, want: "proposed_change"},
		{name: "situation too large", edit: func(event *Event) { event.Situation = strings.Repeat("x", MaxFeedbackTextBytes+1) }, want: "situation"},
		{name: "details too large", edit: func(event *Event) { event.Details = strings.Repeat("x", MaxFeedbackTextBytes+1) }, want: "details"},
		{name: "proposed change too large", edit: func(event *Event) { event.ProposedChange = strings.Repeat("x", MaxFeedbackTextBytes+1) }, want: "proposed_change"},
		{name: "agent too large", edit: func(event *Event) { event.Agent = strings.Repeat("x", MaxAgentBytes+1) }, want: "agent"},
		{name: "event too large", edit: func(event *Event) { event.IdempotencyKey = strings.Repeat("x", MaxEventBytes) }, want: "activity event"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := base
			test.edit(&event)
			err := ValidateEvent(event)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want field %q", err, test.want)
			}
		})
	}
}

func TestUnknownEventFieldsAreIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	event := validEvent(EventRetrieval, "evt_unknown_field", time.Date(2026, 7, 31, 18, 0, 0, 0, time.UTC))
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["future_metadata"] = map[string]any{"value": true}
	data, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	scan, err := scanEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Warnings) != 0 || len(scan.Events) != 1 || !reflect.DeepEqual(scan.Events[0], event) {
		t.Fatalf("scan = %#v", scan)
	}
}

func TestActivityEventRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	start := time.Date(2026, 7, 31, 18, 0, 0, 123456000, time.UTC)
	events := []Event{
		validEvent(EventRetrieval, "evt_001", start),
		validEvent(EventUse, "evt_002", start.Add(time.Second)),
		validEvent(EventFeedback, "evt_003", start.Add(2*time.Second)),
	}
	events[2].Sentiment = SentimentPositive
	events[2].Situation = "Reviewing a Go change."
	events[2].Details = "The checklist caught an inline review thread."
	events[2].ProposedChange = "Preserve the inline review step."

	for _, event := range events {
		result, err := appendEvent(path, event)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Recorded || result.Duplicate || !reflect.DeepEqual(result.Event, event) {
			t.Fatalf("append result = %#v", result)
		}
	}

	scan, err := scanEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Warnings) != 0 || !reflect.DeepEqual(scan.Events, events) {
		t.Fatalf("round trip = %#v", scan)
	}
}

func TestGeneratedEventsHaveStableIdentityTimestampAndRevisionSnapshot(t *testing.T) {
	cfg := identityTestConfig(t)
	writeInstalledSkill(t, cfg, "redis", "Redis")
	identity, err := ResolveSkill(cfg, "redis")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordRetrieval(cfg, identity, ChannelCLI, "show", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordUse(cfg, UseInput{SkillID: "redis", Channel: ChannelMCP, Operation: "record_skill_use"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordFeedback(cfg, FeedbackInput{
		SkillID:        "redis",
		Sentiment:      SentimentPositive,
		Situation:      "Applying the skill to a real task",
		Details:        "The guidance worked as intended",
		ProposedChange: "Keep the effective guidance",
		Channel:        ChannelMCP,
		Operation:      "record_skill_feedback",
	}); err != nil {
		t.Fatal(err)
	}

	scan, err := scanEvents(cfg.StatsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Events) != 3 || len(scan.Warnings) != 0 {
		t.Fatalf("generated events = %#v warnings=%#v", scan.Events, scan.Warnings)
	}
	seenIDs := map[string]bool{}
	for _, event := range scan.Events {
		if event.SkillKey == "" || event.SkillKey != identity.SkillKey || event.Timestamp.IsZero() || event.Timestamp.Location() != time.UTC {
			t.Fatalf("event identity or timestamp = %#v", event)
		}
		if seenIDs[event.EventID] {
			t.Fatalf("duplicate event ID %s", event.EventID)
		}
		seenIDs[event.EventID] = true
		if event.EventType == EventFeedback && event.SkillHash != identity.SkillHash {
			t.Fatalf("feedback skill hash = %q, want %q", event.SkillHash, identity.SkillHash)
		}
	}
}

func validEvent(eventType EventType, eventID string, timestamp time.Time) Event {
	return Event{
		SchemaVersion: SchemaVersion,
		EventID:       eventID,
		EventType:     eventType,
		SkillKey:      "sha256:skill-key",
		SkillID:       "redis",
		SkillName:     "Redis",
		SkillHash:     "sha256:revision",
		SourceID:      "src_example",
		Timestamp:     timestamp,
		Channel:       ChannelCLI,
		Operation:     "test",
		Agent:         "codex",
	}
}
