package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/feedback"
)

func feedbackPost(chat int64, message int) feedback.Post {
	item := fact(chat, "example:one")
	item.SourceEvidence, item.AIProvider, item.AIModel, item.GenerationPolicy = "public evidence", "gemini", "test-model", daily.FactPolicy
	return feedback.Post{ChatID: chat, MessageID: message, Publication: feedback.Scheduled, PublishedAt: testNow, Item: item}
}

func feedbackEmoji(value string, count int64) feedback.Reaction {
	return feedback.Reaction{Type: feedback.Emoji, Value: value, Count: count}
}

func feedbackEvent(id int64, mode string, before, after []feedback.Reaction) feedback.Event {
	return feedback.Event{UpdateID: id, ChatID: -1, MessageID: 99, Mode: mode,
		OccurredAt: testNow.Add(time.Duration(id) * time.Second).Unix(), ReceivedAt: testNow.Add(time.Hour), Before: before, After: after}
}

func reactionCount(t *testing.T, s *Store, chat int64, message int, emoji string) int64 {
	t.Helper()
	var count int64
	must(t, s.db.QueryRowContext(t.Context(), `SELECT COALESCE((SELECT c.count FROM feedback_counts c
 JOIN feedback_posts p ON p.id=c.post_id WHERE p.chat_id=? AND p.message_id=? AND c.reaction_type='emoji' AND c.reaction_value=?),0)`, chat, message, emoji).Scan(&count))
	return count
}

func TestFeedbackDeltaReplacementRemovalAndReplay(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.RegisterFeedbackPost(ctx, feedbackPost(-1, 99)))
	events := []feedback.Event{
		feedbackEvent(1, feedback.Delta, nil, []feedback.Reaction{feedbackEmoji("👍", 1)}),
		feedbackEvent(2, feedback.Delta, nil, []feedback.Reaction{feedbackEmoji("👍", 1)}),
		feedbackEvent(3, feedback.Delta, []feedback.Reaction{feedbackEmoji("👍", 1)}, []feedback.Reaction{feedbackEmoji("🤣", 1)}),
		feedbackEvent(4, feedback.Delta, []feedback.Reaction{feedbackEmoji("🤣", 1)}, nil),
	}
	for _, event := range events {
		recorded, err := s.RecordFeedback(ctx, event)
		must(t, err)
		if !recorded {
			t.Fatal("new event not recorded", event.UpdateID)
		}
		recorded, err = s.RecordFeedback(ctx, event)
		must(t, err)
		if recorded {
			t.Fatal("replay counted", event.UpdateID)
		}
	}
	if got := reactionCount(t, s, -1, 99, "👍"); got != 1 {
		t.Fatal(got)
	}
	if got := reactionCount(t, s, -1, 99, "🤣"); got != 0 {
		t.Fatal(got)
	}
	var n int
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM feedback_events").Scan(&n))
	if n != 4 {
		t.Fatal(n)
	}
}

func TestFeedbackSnapshotsReplaceCountsAndIgnoreOlderEvents(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.RegisterFeedbackPost(ctx, feedbackPost(-1, 99)))
	for _, event := range []feedback.Event{
		feedbackEvent(10, feedback.Snapshot, nil, []feedback.Reaction{feedbackEmoji("👍", 10), feedbackEmoji("🤣", 5)}),
		feedbackEvent(12, feedback.Snapshot, nil, []feedback.Reaction{feedbackEmoji("👍", 3)}),
		feedbackEvent(11, feedback.Snapshot, nil, []feedback.Reaction{feedbackEmoji("👍", 99)}),
		feedbackEvent(9, feedback.Delta, nil, []feedback.Reaction{feedbackEmoji("👍", 1)}),
	} {
		recorded, err := s.RecordFeedback(ctx, event)
		must(t, err)
		if !recorded {
			t.Fatal(event)
		}
	}
	if got := reactionCount(t, s, -1, 99, "👍"); got != 3 {
		t.Fatal("snapshot added or rolled back", got)
	}
	if got := reactionCount(t, s, -1, 99, "🤣"); got != 0 {
		t.Fatal("removed snapshot reaction retained", got)
	}
	var applied int
	must(t, s.db.QueryRowContext(ctx, "SELECT sum(applied) FROM feedback_events").Scan(&applied))
	if applied != 2 {
		t.Fatal(applied)
	}
	// Empty is a full snapshot too: all reactions have been removed.
	_, err := s.RecordFeedback(ctx, feedbackEvent(13, feedback.Snapshot, nil, nil))
	must(t, err)
	if got := reactionCount(t, s, -1, 99, "👍"); got != 0 {
		t.Fatal(got)
	}
}

func TestFeedbackPrivacyIsolationAndImmutablePost(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	p := feedbackPost(-1, 99)
	must(t, s.RegisterFeedbackPost(ctx, p))
	p.Item.Text, p.Item.AIModel = "changed", "different-model"
	must(t, s.RegisterFeedbackPost(ctx, p))
	must(t, s.RegisterFeedbackPost(ctx, feedbackPost(-2, 99)))
	event := feedbackEvent(1, feedback.Delta, nil, []feedback.Reaction{feedbackEmoji("👍", 1)})
	_, err := s.RecordFeedback(ctx, event)
	must(t, err)
	event.ChatID, event.UpdateID = -3, 2
	recorded, err := s.RecordFeedback(ctx, event)
	must(t, err)
	if recorded || reactionCount(t, s, -2, 99, "👍") != 0 {
		t.Fatal("unknown post or other chat affected")
	}
	must(t, s.RegisterFeedbackPost(ctx, feedbackPost(-1, 100)))
	event.ChatID, event.MessageID, event.UpdateID = -1, 100, 3
	_, err = s.RecordFeedback(ctx, event)
	must(t, err)
	if reactionCount(t, s, -1, 100, "👍") != 1 || reactionCount(t, s, -1, 99, "👍") != 1 {
		t.Fatal("other message in same chat affected")
	}
	var text, model, before, after string
	must(t, s.db.QueryRowContext(ctx, "SELECT text,ai_model FROM feedback_posts WHERE chat_id=-1 AND message_id=99").Scan(&text, &model))
	if text != "Факт" || model != "test-model" {
		t.Fatal("snapshot overwritten", text, model)
	}
	must(t, s.db.QueryRowContext(ctx, "SELECT before_json,after_json FROM feedback_events WHERE update_id=1").Scan(&before, &after))
	var values []map[string]any
	must(t, json.Unmarshal([]byte(after), &values))
	if before != "[]" || len(values) != 1 || len(values[0]) != 3 {
		t.Fatal("unexpected event data", before, after)
	}
}

func TestFeedbackOutOfOrderDeltasRetainPartialEvidence(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.RegisterFeedbackPost(ctx, feedbackPost(-1, 99)))
	_, err := s.RecordFeedback(ctx, feedbackEvent(2, feedback.Delta, []feedback.Reaction{feedbackEmoji("👍", 1)}, nil))
	must(t, err)
	if reactionCount(t, s, -1, 99, "👍") != -1 {
		t.Fatal("missing addition concealed")
	}
	_, err = s.RecordFeedback(ctx, feedbackEvent(1, feedback.Delta, nil, []feedback.Reaction{feedbackEmoji("👍", 1)}))
	must(t, err)
	if reactionCount(t, s, -1, 99, "👍") != 0 {
		t.Fatal("out of order delta lost")
	}
	var partial int
	must(t, s.db.QueryRowContext(ctx, "SELECT partial FROM feedback_posts").Scan(&partial))
	if partial != 1 {
		t.Fatal("incomplete stream not marked")
	}
}

func TestFeedbackMigrationAndRestart(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "feedback.db")
	s, err := Open(ctx, path)
	must(t, err)
	p, err := s.migrator()
	must(t, err)
	_, err = p.DownTo(ctx, 17)
	must(t, err)
	must(t, s.EnsureChat(ctx, -1))
	_, err = s.db.ExecContext(ctx, `INSERT INTO items(id,chat_id,kind,text,source,content_key,author_id,state,created_at)
 VALUES(1,-1,'fact','Legacy','https://example.org','legacy',42,'pending',1)`)
	must(t, err)
	_, err = p.Up(ctx)
	must(t, err)
	item, err := s.Item(ctx, -1, 1)
	must(t, err)
	if item.AIModel != "" {
		t.Fatal("legacy model guessed", item)
	}
	must(t, s.RegisterFeedbackPost(ctx, feedbackPost(-1, 99)))
	event := feedbackEvent(1, feedback.Delta, nil, []feedback.Reaction{feedbackEmoji("👍", 1)})
	_, err = s.RecordFeedback(ctx, event)
	must(t, err)
	must(t, s.Close())
	s, err = Open(ctx, path)
	must(t, err)
	defer s.Close()
	recorded, err := s.RecordFeedback(ctx, event)
	must(t, err)
	if recorded || reactionCount(t, s, -1, 99, "👍") != 1 {
		t.Fatal("restart lost idempotency")
	}
}

func TestFeedbackMixedModeAndSameSecondOrdering(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.RegisterFeedbackPost(ctx, feedbackPost(-1, 99)))
	first := feedbackEvent(1, feedback.Snapshot, nil, []feedback.Reaction{feedbackEmoji("👍", 3)})
	second := feedbackEvent(2, feedback.Delta, nil, []feedback.Reaction{feedbackEmoji("👍", 1)})
	second.OccurredAt = first.OccurredAt
	_, err := s.RecordFeedback(ctx, first)
	must(t, err)
	_, err = s.RecordFeedback(ctx, second)
	must(t, err)
	if reactionCount(t, s, -1, 99, "👍") != 4 {
		t.Fatal("new delta swallowed by same-second snapshot")
	}
	var partial int
	must(t, s.db.QueryRowContext(ctx, "SELECT partial FROM feedback_posts").Scan(&partial))
	if partial != 1 {
		t.Fatal("changed counter mode not marked")
	}
}

func TestPreparedAIModelSurvivesStorageUntilPreview(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	i := fact(-1, "model-persistence")
	i.AIProvider, i.AIModel, i.GenerationPolicy = "groq", "prepared-model", daily.FactPolicy
	_, err := s.SaveAIItems(ctx, -1, daily.Fact, []daily.Item{i}, testNow)
	must(t, err)
	preview, found, err := s.PreviewAIItem(ctx, -1, daily.Fact, testNow)
	must(t, err)
	if !found || preview.AIModel != i.AIModel || preview.AIProvider != i.AIProvider {
		t.Fatal(preview, found)
	}
}

func TestFeedbackTransactionFailureCanBeRetriedWithoutDoubleCounting(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.RegisterFeedbackPost(ctx, feedbackPost(-1, 99)))
	_, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_feedback_count BEFORE INSERT ON feedback_counts
 BEGIN SELECT RAISE(ABORT,'test storage failure'); END`)
	must(t, err)
	event := feedbackEvent(1, feedback.Delta, nil, []feedback.Reaction{feedbackEmoji("👍", 1)})
	recorded, err := s.RecordFeedback(ctx, event)
	if err == nil || recorded {
		t.Fatal("failure committed event", recorded, err)
	}
	var n int
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM feedback_events").Scan(&n))
	if n != 0 {
		t.Fatal("event persisted without its counts", n)
	}
	_, err = s.db.ExecContext(ctx, "DROP TRIGGER reject_feedback_count")
	must(t, err)
	recorded, err = s.RecordFeedback(ctx, event)
	must(t, err)
	if !recorded || reactionCount(t, s, -1, 99, "👍") != 1 {
		t.Fatal(recorded)
	}
}

func TestMultipleEmojiChangeDoesNotCountAnUnchangedReactionTwice(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.RegisterFeedbackPost(ctx, feedbackPost(-1, 99)))
	initial := []feedback.Reaction{feedbackEmoji("👍", 1), feedbackEmoji("🤣", 1)}
	_, err := s.RecordFeedback(ctx, feedbackEvent(1, feedback.Delta, nil, initial))
	must(t, err)
	_, err = s.RecordFeedback(ctx, feedbackEvent(2, feedback.Delta, initial, []feedback.Reaction{feedbackEmoji("👍", 1), feedbackEmoji("🔥", 1)}))
	must(t, err)
	if reactionCount(t, s, -1, 99, "👍") != 1 || reactionCount(t, s, -1, 99, "🤣") != 0 || reactionCount(t, s, -1, 99, "🔥") != 1 {
		t.Fatal("incorrect multi-reaction replacement")
	}
}
