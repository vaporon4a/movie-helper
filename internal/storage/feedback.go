package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/vaporon4a/movie-helper/internal/feedback"
)

func (s *Store) RegisterFeedbackPost(ctx context.Context, p feedback.Post) error {
	if err := p.Validate(); err != nil {
		return err
	}
	i := p.Item
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO feedback_posts
 (chat_id,message_id,publication,published_at,kind,item_id,content_key,text,source,image,
 source_evidence,ai_provider,ai_model,generation_policy) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ChatID, p.MessageID, p.Publication, p.PublishedAt.Unix(), i.Kind, i.ID, i.Key,
		i.Text, i.Source, i.Image, i.SourceEvidence, i.AIProvider, i.AIModel, i.GenerationPolicy)
	return err
}

type feedbackState struct {
	id, lastAt, lastID, snapshotAt, snapshotID int64
	mode                                       string
}

// RecordFeedback is transactional: an update is either stored and counted once,
// or not committed. Unknown messages never acquire a feedback record.
func (s *Store) RecordFeedback(ctx context.Context, event feedback.Event) (bool, error) {
	if err := event.Validate(); err != nil {
		return false, err
	}
	var recorded bool
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		state, err := loadFeedbackState(ctx, tx, event)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		apply := shouldApplyFeedback(state, event)
		recorded, err = insertFeedbackEvent(ctx, tx, state.id, event, apply)
		if err != nil || !recorded || !apply {
			return err
		}
		return applyFeedback(ctx, tx, state, event)
	})
	return recorded && err == nil, err
}

func loadFeedbackState(ctx context.Context, tx *sql.Tx, event feedback.Event) (feedbackState, error) {
	var state feedbackState
	err := tx.QueryRowContext(ctx, `SELECT id,counter_mode,last_event_at,last_update_id,snapshot_at,snapshot_update_id
 FROM feedback_posts WHERE chat_id=? AND message_id=?`, event.ChatID, event.MessageID).
		Scan(&state.id, &state.mode, &state.lastAt, &state.lastID, &state.snapshotAt, &state.snapshotID)
	return state, err
}

func shouldApplyFeedback(state feedbackState, event feedback.Event) bool {
	if event.Mode == feedback.Snapshot {
		return newerFeedback(event.OccurredAt, event.UpdateID, state.lastAt, state.lastID)
	}
	// A snapshot already incorporates older deltas; applying them again doubles counts.
	return newerFeedback(event.OccurredAt, event.UpdateID, state.snapshotAt, state.snapshotID)
}

func newerFeedback(at, id, previousAt, previousID int64) bool {
	return at > previousAt || (at == previousAt && id > previousID)
}

func insertFeedbackEvent(ctx context.Context, tx *sql.Tx, post int64, event feedback.Event, apply bool) (bool, error) {
	before, err := encodeReactions(event.Before)
	if err != nil {
		return false, err
	}
	after, err := encodeReactions(event.After)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO feedback_events
 (update_id,post_id,mode,occurred_at,received_at,before_json,after_json,applied) VALUES(?,?,?,?,?,?,?,?)`,
		event.UpdateID, post, event.Mode, event.OccurredAt, event.ReceivedAt.Unix(), before, after, apply)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func encodeReactions(reactions []feedback.Reaction) (string, error) {
	if reactions == nil {
		reactions = []feedback.Reaction{}
	}
	data, err := json.Marshal(reactions)
	return string(data), err
}

func applyFeedback(ctx context.Context, tx *sql.Tx, state feedbackState, event feedback.Event) error {
	if event.Mode == feedback.Snapshot {
		if _, err := tx.ExecContext(ctx, "DELETE FROM feedback_counts WHERE post_id=?", state.id); err != nil {
			return err
		}
		state.snapshotAt, state.snapshotID = event.OccurredAt, event.UpdateID
	} else if err := addFeedbackCounts(ctx, tx, state.id, event.Before, -1); err != nil {
		return err
	}
	if err := addFeedbackCounts(ctx, tx, state.id, event.After, 1); err != nil {
		return err
	}
	if newerFeedback(event.OccurredAt, event.UpdateID, state.lastAt, state.lastID) {
		state.lastAt, state.lastID = event.OccurredAt, event.UpdateID
	}
	// Missing additions must remain visible as partial data, not a fabricated zero.
	_, err := tx.ExecContext(ctx, `UPDATE feedback_posts SET counter_mode=?,last_event_at=?,last_update_id=?,
 snapshot_at=?,snapshot_update_id=?,partial=partial OR (? AND counter_mode!='' AND counter_mode!=?)
 OR EXISTS(SELECT 1 FROM feedback_counts WHERE post_id=? AND count<0) WHERE id=?`,
		event.Mode, state.lastAt, state.lastID, state.snapshotAt, state.snapshotID,
		event.Mode != state.mode, event.Mode, state.id, state.id)
	return err
}

func addFeedbackCounts(ctx context.Context, tx *sql.Tx, post int64, reactions []feedback.Reaction, sign int64) error {
	for _, r := range reactions {
		_, err := tx.ExecContext(ctx, `INSERT INTO feedback_counts(post_id,reaction_type,reaction_value,count)
 VALUES(?,?,?,?) ON CONFLICT(post_id,reaction_type,reaction_value) DO UPDATE SET count=count+excluded.count`,
			post, r.Type, r.Value, sign*r.Count)
		if err != nil {
			return err
		}
	}
	return nil
}
