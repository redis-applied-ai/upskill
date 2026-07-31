package activity

import (
	"sort"
	"time"
)

func statsForIdentity(path string, identity SkillIdentity, feedbackLimit int) (SkillStats, error) {
	scan, err := scanEvents(path)
	if err != nil {
		return SkillStats{}, err
	}
	return aggregate(identity, scan.Events, feedbackLimit, scan.Warnings), nil
}

func aggregate(identity SkillIdentity, events []Event, feedbackLimit int, warnings []string) SkillStats {
	if feedbackLimit < 0 {
		feedbackLimit = 0
	}
	if feedbackLimit > MaxFeedbackLimit {
		feedbackLimit = MaxFeedbackLimit
	}
	stats := SkillStats{
		SkillKey:         identity.SkillKey,
		SkillID:          identity.SkillID,
		SkillName:        identity.SkillName,
		CurrentSkillHash: identity.SkillHash,
		RecentFeedback:   []Feedback{},
		Warnings:         append([]string{}, warnings...),
	}
	allFeedback := make([]Feedback, 0)
	for _, event := range events {
		if event.SkillKey != identity.SkillKey {
			continue
		}
		switch event.EventType {
		case EventRetrieval:
			stats.RetrievalCount++
			updateFirstLast(&stats.FirstRetrievedAt, &stats.LastRetrievedAt, event.Timestamp)
		case EventUse:
			stats.UseCount++
			updateFirstLast(&stats.FirstUsedAt, &stats.LastUsedAt, event.Timestamp)
		case EventFeedback:
			stats.FeedbackCount++
			if event.Sentiment == SentimentPositive {
				stats.PositiveFeedbackCount++
			} else if event.Sentiment == SentimentNegative {
				stats.NegativeFeedbackCount++
			}
			if stats.LastFeedbackAt == nil || event.Timestamp.After(*stats.LastFeedbackAt) {
				value := event.Timestamp
				stats.LastFeedbackAt = &value
			}
			allFeedback = append(allFeedback, Feedback{
				EventID:        event.EventID,
				Sentiment:      event.Sentiment,
				Situation:      event.Situation,
				Details:        event.Details,
				ProposedChange: event.ProposedChange,
				SkillHash:      event.SkillHash,
				Timestamp:      event.Timestamp,
				Agent:          event.Agent,
			})
		}
	}
	sort.SliceStable(allFeedback, func(i, j int) bool {
		if allFeedback[i].Timestamp.Equal(allFeedback[j].Timestamp) {
			return allFeedback[i].EventID > allFeedback[j].EventID
		}
		return allFeedback[i].Timestamp.After(allFeedback[j].Timestamp)
	})
	if feedbackLimit > len(allFeedback) {
		feedbackLimit = len(allFeedback)
	}
	stats.RecentFeedback = append(stats.RecentFeedback, allFeedback[:feedbackLimit]...)
	return stats
}

func updateFirstLast(first, last **time.Time, value time.Time) {
	if *first == nil || value.Before(**first) {
		copy := value
		*first = &copy
	}
	if *last == nil || value.After(**last) {
		copy := value
		*last = &copy
	}
}
