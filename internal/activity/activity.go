package activity

import (
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"upskill/internal/config"
)

var (
	eventIDMu      sync.Mutex
	eventIDEntropy = ulid.Monotonic(rand.Reader, 0)
	activityNow    = func() time.Time { return time.Now().UTC() }
)

func Stats(cfg config.Config, skillID string, feedbackLimit int) (SkillStats, error) {
	identity, err := resolveSkillMetadata(cfg, skillID)
	if err != nil {
		return SkillStats{}, err
	}
	if feedbackLimit < 0 {
		return SkillStats{}, fmt.Errorf("feedback_limit must not be negative")
	}
	if feedbackLimit > MaxFeedbackLimit {
		feedbackLimit = MaxFeedbackLimit
	}
	return statsForIdentity(cfg.StatsPath, identity, feedbackLimit)
}

func RecordRetrieval(cfg config.Config, identity SkillIdentity, channel Channel, operation, agent string) (SkillStats, error) {
	event, err := newEvent(identity, EventRetrieval, channel, operation, agent, "")
	if err != nil {
		return SkillStats{}, err
	}
	if _, err := appendEvent(cfg.StatsPath, event); err != nil {
		return SkillStats{}, err
	}
	return statsForIdentity(cfg.StatsPath, identity, DefaultFeedbackLimit)
}

func RecordUse(cfg config.Config, input UseInput) (RecordResult, error) {
	if err := ValidateUseInput(input); err != nil {
		return RecordResult{}, err
	}
	identity, err := ResolveSkill(cfg, input.SkillID)
	if err != nil {
		return RecordResult{}, err
	}
	event, err := newEvent(identity, EventUse, defaultChannel(input.Channel), defaultOperation(input.Operation, "stats_use"), input.Agent, input.IdempotencyKey)
	if err != nil {
		return RecordResult{}, err
	}
	appended, err := appendEvent(cfg.StatsPath, event)
	if err != nil {
		return RecordResult{}, err
	}
	stats, err := statsForIdentity(cfg.StatsPath, identity, 0)
	if err != nil {
		return RecordResult{}, err
	}
	return RecordResult{
		Recorded:       appended.Recorded,
		Duplicate:      appended.Duplicate,
		EventID:        appended.Event.EventID,
		SkillID:        identity.SkillID,
		RetrievalCount: stats.RetrievalCount,
		UseCount:       stats.UseCount,
	}, nil
}

func RecordFeedback(cfg config.Config, input FeedbackInput) (RecordResult, error) {
	if err := ValidateFeedbackInput(input); err != nil {
		return RecordResult{}, err
	}
	identity, err := ResolveSkill(cfg, input.SkillID)
	if err != nil {
		return RecordResult{}, err
	}
	event, err := newEvent(identity, EventFeedback, defaultChannel(input.Channel), defaultOperation(input.Operation, "stats_feedback"), input.Agent, input.IdempotencyKey)
	if err != nil {
		return RecordResult{}, err
	}
	event.Sentiment = input.Sentiment
	event.Situation = input.Situation
	event.Details = input.Details
	event.ProposedChange = input.ProposedChange
	if err := ValidateEvent(event); err != nil {
		return RecordResult{}, err
	}
	appended, err := appendEvent(cfg.StatsPath, event)
	if err != nil {
		return RecordResult{}, err
	}
	stats, err := statsForIdentity(cfg.StatsPath, identity, 0)
	if err != nil {
		return RecordResult{}, err
	}
	return RecordResult{
		Recorded:              appended.Recorded,
		Duplicate:             appended.Duplicate,
		EventID:               appended.Event.EventID,
		SkillID:               identity.SkillID,
		Sentiment:             appended.Event.Sentiment,
		RetrievalCount:        stats.RetrievalCount,
		UseCount:              stats.UseCount,
		FeedbackCount:         stats.FeedbackCount,
		PositiveFeedbackCount: stats.PositiveFeedbackCount,
		NegativeFeedbackCount: stats.NegativeFeedbackCount,
	}, nil
}

func newEvent(identity SkillIdentity, eventType EventType, channel Channel, operation, agent, idempotencyKey string) (Event, error) {
	now := activityNow().UTC()
	eventIDMu.Lock()
	id, err := ulid.New(ulid.Timestamp(now), eventIDEntropy)
	eventIDMu.Unlock()
	if err != nil {
		return Event{}, fmt.Errorf("generate activity event id: %w", err)
	}
	event := Event{
		SchemaVersion:  SchemaVersion,
		EventID:        "evt_" + strings.ToLower(id.String()),
		EventType:      eventType,
		SkillKey:       identity.SkillKey,
		SkillID:        identity.SkillID,
		SkillName:      identity.SkillName,
		SkillHash:      identity.SkillHash,
		SourceID:       identity.SourceID,
		Timestamp:      now,
		Channel:        channel,
		Operation:      operation,
		Agent:          agent,
		IdempotencyKey: idempotencyKey,
	}
	if eventType != EventFeedback {
		if err := ValidateEvent(event); err != nil {
			return Event{}, err
		}
	}
	return event, nil
}

func defaultChannel(channel Channel) Channel {
	if channel == "" {
		return ChannelCLI
	}
	return channel
}

func defaultOperation(operation, fallback string) string {
	if strings.TrimSpace(operation) == "" {
		return fallback
	}
	return operation
}
