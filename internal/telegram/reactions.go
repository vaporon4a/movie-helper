package telegram

import (
	"context"

	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/feedback"
)

func (h *Handler) handleReactions(ctx context.Context, update *models.Update) {
	if h.Feedback == nil {
		return
	}
	event, ok := reactionEvent(update)
	if !ok || !h.Allowed[event.ChatID] {
		return
	}
	event.ReceivedAt = h.Now()
	recorded, err := h.Feedback.RecordFeedback(ctx, event)
	if err != nil {
		h.Log.Warn("reaction feedback not recorded", "chat_id", event.ChatID,
			"message_id", event.MessageID, "update_id", event.UpdateID, "mode", event.Mode)
		return
	}
	h.Log.Debug("reaction feedback processed", "chat_id", event.ChatID,
		"message_id", event.MessageID, "update_id", event.UpdateID, "mode", event.Mode, "recorded", recorded)
}

func reactionEvent(update *models.Update) (feedback.Event, bool) {
	event := feedback.Event{UpdateID: update.ID}
	if r := update.MessageReaction; r != nil {
		if !reactionGroup(r.Chat) || (r.User != nil && r.User.IsBot) {
			return event, false
		}
		event.ChatID, event.MessageID, event.OccurredAt, event.Mode = r.Chat.ID, r.MessageID, int64(r.Date), feedback.Delta
		before, ok := reactionSet(r.OldReaction)
		if !ok {
			return event, false
		}
		after, ok := reactionSet(r.NewReaction)
		event.Before, event.After = before, after
		return event, ok
	}
	if r := update.MessageReactionCount; r != nil {
		if !reactionGroup(r.Chat) {
			return event, false
		}
		event.ChatID, event.MessageID, event.OccurredAt, event.Mode = r.Chat.ID, r.MessageID, int64(r.Date), feedback.Snapshot
		event.After = make([]feedback.Reaction, 0, len(r.Reactions))
		for _, count := range r.Reactions {
			reaction, ok := reactionValue(count.Type)
			if !ok {
				return event, false
			}
			reaction.Count = int64(count.TotalCount)
			event.After = append(event.After, reaction)
		}
		return event, true
	}
	return event, false
}

func reactionGroup(chat models.Chat) bool {
	return chat.Type == models.ChatTypeGroup || chat.Type == models.ChatTypeSupergroup
}

func reactionSet(set []models.ReactionType) ([]feedback.Reaction, bool) {
	result := make([]feedback.Reaction, 0, len(set))
	for _, r := range set {
		reaction, ok := reactionValue(r)
		if !ok {
			return nil, false
		}
		reaction.Count = 1
		result = append(result, reaction)
	}
	return result, true
}

func reactionValue(r models.ReactionType) (feedback.Reaction, bool) {
	switch r.Type {
	case models.ReactionTypeTypeEmoji:
		if r.ReactionTypeEmoji != nil {
			return feedback.Reaction{Type: feedback.Emoji, Value: r.ReactionTypeEmoji.Emoji}, true
		}
	case models.ReactionTypeTypeCustomEmoji:
		if r.ReactionTypeCustomEmoji != nil {
			return feedback.Reaction{Type: feedback.Custom, Value: r.ReactionTypeCustomEmoji.CustomEmojiID}, true
		}
	case models.ReactionTypeTypePaid:
		return feedback.Reaction{Type: feedback.Paid}, true
	}
	return feedback.Reaction{}, false
}
