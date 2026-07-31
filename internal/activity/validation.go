package activity

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ValidateEvent(event Event) error {
	if event.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %d", SchemaVersion)
	}
	if strings.TrimSpace(event.EventID) == "" {
		return fmt.Errorf("event_id is required")
	}
	if !validEventType(event.EventType) {
		return fmt.Errorf("event_type must be retrieval, use, or feedback")
	}
	if strings.TrimSpace(event.SkillKey) == "" {
		return fmt.Errorf("skill_key is required")
	}
	if strings.TrimSpace(event.SkillID) == "" {
		return fmt.Errorf("skill_id is required")
	}
	if event.Timestamp.IsZero() {
		return fmt.Errorf("timestamp is required")
	}
	if event.Channel != "" && event.Channel != ChannelCLI && event.Channel != ChannelMCP {
		return fmt.Errorf("channel must be cli or mcp")
	}
	if len(event.Agent) > MaxAgentBytes {
		return fmt.Errorf("agent exceeds %d bytes", MaxAgentBytes)
	}

	if event.EventType == EventFeedback {
		if err := validateFeedbackFields(event.Sentiment, event.Situation, event.Details, event.ProposedChange); err != nil {
			return err
		}
	} else if event.Sentiment != "" || event.Situation != "" || event.Details != "" || event.ProposedChange != "" {
		return fmt.Errorf("feedback fields require event_type feedback")
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("serialize activity event: %w", err)
	}
	if len(data)+1 > MaxEventBytes {
		return fmt.Errorf("activity event exceeds %d bytes", MaxEventBytes)
	}
	return nil
}

func ValidateFeedbackInput(input FeedbackInput) error {
	if strings.TrimSpace(input.SkillID) == "" {
		return fmt.Errorf("skill_id is required")
	}
	if len(input.Agent) > MaxAgentBytes {
		return fmt.Errorf("agent exceeds %d bytes", MaxAgentBytes)
	}
	return validateFeedbackFields(input.Sentiment, input.Situation, input.Details, input.ProposedChange)
}

func ValidateUseInput(input UseInput) error {
	if strings.TrimSpace(input.SkillID) == "" {
		return fmt.Errorf("skill_id is required")
	}
	if len(input.Agent) > MaxAgentBytes {
		return fmt.Errorf("agent exceeds %d bytes", MaxAgentBytes)
	}
	return nil
}

func validateFeedbackFields(sentiment Sentiment, situation, details, proposedChange string) error {
	if sentiment != SentimentPositive && sentiment != SentimentNegative {
		return fmt.Errorf("sentiment must be positive or negative")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "situation", value: situation},
		{name: "details", value: details},
		{name: "proposed_change", value: proposedChange},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
		if len(field.value) > MaxFeedbackTextBytes {
			return fmt.Errorf("%s exceeds %d bytes", field.name, MaxFeedbackTextBytes)
		}
	}
	return nil
}

func validEventType(eventType EventType) bool {
	switch eventType {
	case EventRetrieval, EventUse, EventFeedback:
		return true
	default:
		return false
	}
}
