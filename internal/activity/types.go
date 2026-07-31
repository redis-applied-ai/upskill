// Package activity records and aggregates local installed-skill activity.
package activity

import (
	"encoding/json"
	"time"
)

const (
	SchemaVersion = 1

	MaxFeedbackTextBytes = 16 * 1024
	MaxAgentBytes        = 256
	MaxEventBytes        = 64 * 1024
	DefaultFeedbackLimit = 10
	MaxFeedbackLimit     = 100
)

type EventType string

const (
	EventRetrieval EventType = "retrieval"
	EventUse       EventType = "use"
	EventFeedback  EventType = "feedback"
)

type Sentiment string

const (
	SentimentPositive Sentiment = "positive"
	SentimentNegative Sentiment = "negative"
)

type Channel string

const (
	ChannelCLI Channel = "cli"
	ChannelMCP Channel = "mcp"
)

// Event is the version 1 JSON Lines activity record.
type Event struct {
	SchemaVersion  int       `json:"schema_version"`
	EventID        string    `json:"event_id"`
	EventType      EventType `json:"event_type"`
	SkillKey       string    `json:"skill_key"`
	SkillID        string    `json:"skill_id"`
	SkillName      string    `json:"skill_name,omitempty"`
	SkillHash      string    `json:"skill_hash,omitempty"`
	SourceID       string    `json:"source_id,omitempty"`
	Timestamp      time.Time `json:"timestamp"`
	Channel        Channel   `json:"channel,omitempty"`
	Operation      string    `json:"operation,omitempty"`
	Agent          string    `json:"agent,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	Sentiment      Sentiment `json:"sentiment,omitempty"`
	Situation      string    `json:"situation,omitempty"`
	Details        string    `json:"details,omitempty"`
	ProposedChange string    `json:"proposed_change,omitempty"`
}

type SkillIdentity struct {
	SkillKey  string `json:"skill_key"`
	SkillID   string `json:"skill_id"`
	SkillName string `json:"skill_name,omitempty"`
	SkillHash string `json:"skill_hash,omitempty"`
	SourceID  string `json:"source_id,omitempty"`
	Path      string `json:"-"`
}

type Feedback struct {
	EventID        string    `json:"event_id"`
	Sentiment      Sentiment `json:"sentiment"`
	Situation      string    `json:"situation"`
	Details        string    `json:"details"`
	ProposedChange string    `json:"proposed_change"`
	SkillHash      string    `json:"skill_hash,omitempty"`
	Timestamp      time.Time `json:"timestamp"`
	Agent          string    `json:"agent,omitempty"`
}

type SkillStats struct {
	SkillKey              string     `json:"skill_key"`
	SkillID               string     `json:"skill_id"`
	SkillName             string     `json:"skill_name,omitempty"`
	CurrentSkillHash      string     `json:"current_skill_hash,omitempty"`
	RetrievalCount        uint64     `json:"retrieval_count"`
	UseCount              uint64     `json:"use_count"`
	FeedbackCount         uint64     `json:"feedback_count"`
	PositiveFeedbackCount uint64     `json:"positive_feedback_count"`
	NegativeFeedbackCount uint64     `json:"negative_feedback_count"`
	FirstRetrievedAt      *time.Time `json:"first_retrieved_at,omitempty"`
	LastRetrievedAt       *time.Time `json:"last_retrieved_at,omitempty"`
	FirstUsedAt           *time.Time `json:"first_used_at,omitempty"`
	LastUsedAt            *time.Time `json:"last_used_at,omitempty"`
	LastFeedbackAt        *time.Time `json:"last_feedback_at,omitempty"`
	RecentFeedback        []Feedback `json:"recent_feedback"`
	Warnings              []string   `json:"warnings"`
}

type UseInput struct {
	SkillID        string
	Agent          string
	IdempotencyKey string
	Channel        Channel
	Operation      string
}

type FeedbackInput struct {
	SkillID        string
	Sentiment      Sentiment
	Situation      string
	Details        string
	ProposedChange string
	Agent          string
	IdempotencyKey string
	Channel        Channel
	Operation      string
}

type RecordResult struct {
	Recorded              bool      `json:"recorded"`
	Duplicate             bool      `json:"duplicate"`
	EventID               string    `json:"event_id"`
	SkillID               string    `json:"skill_id"`
	Sentiment             Sentiment `json:"sentiment,omitempty"`
	RetrievalCount        uint64    `json:"retrieval_count"`
	UseCount              uint64    `json:"use_count"`
	FeedbackCount         uint64    `json:"feedback_count,omitempty"`
	PositiveFeedbackCount uint64    `json:"positive_feedback_count,omitempty"`
	NegativeFeedbackCount uint64    `json:"negative_feedback_count,omitempty"`
}

// MarshalJSON keeps use results compact while making the feedback result shape
// stable even when one sentiment count is zero.
func (result RecordResult) MarshalJSON() ([]byte, error) {
	payload := map[string]any{
		"recorded":        result.Recorded,
		"duplicate":       result.Duplicate,
		"event_id":        result.EventID,
		"skill_id":        result.SkillID,
		"retrieval_count": result.RetrievalCount,
		"use_count":       result.UseCount,
	}
	if result.Sentiment != "" {
		payload["sentiment"] = result.Sentiment
		payload["feedback_count"] = result.FeedbackCount
		payload["positive_feedback_count"] = result.PositiveFeedbackCount
		payload["negative_feedback_count"] = result.NegativeFeedbackCount
	}
	return json.Marshal(payload)
}
