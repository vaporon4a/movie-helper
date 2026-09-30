package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vaporon4a/movie-helper/internal/aiwork"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/featurevote"
)

const (
	backgroundStockTarget = 5
	backgroundStockLow    = 3
	memeStockTTL          = 7 * 24 * time.Hour
)

func (s *Store) EnqueueAIWork(ctx context.Context, kind string, scopeID int64, key string, priority int, now time.Time, urgent bool) error {
	if !validAIWorkKind(kind) || key == "" {
		return errors.New("invalid AI work")
	}
	next := now.Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_work(kind,scope_id,work_key,state,priority,next_attempt,created_at,updated_at)
 VALUES(?,?,?,'pending',?,?,?,?)
 ON CONFLICT(work_key) DO UPDATE SET
  priority=CASE WHEN ai_work.state='done' THEN excluded.priority ELSE MAX(ai_work.priority,excluded.priority) END,
  state=CASE WHEN ai_work.state='running' THEN 'running' WHEN ai_work.state='done' OR ? THEN 'pending' ELSE ai_work.state END,
  next_attempt=CASE WHEN ai_work.state='running' THEN ai_work.next_attempt WHEN ai_work.state='done' OR ? THEN excluded.next_attempt ELSE ai_work.next_attempt END,
  attempts=CASE WHEN ai_work.state='done' THEN 0 ELSE ai_work.attempts END,
  last_error_code=CASE WHEN ai_work.state='done' THEN '' ELSE ai_work.last_error_code END,
  updated_at=excluded.updated_at`, kind, scopeID, key, priority, next, next, next, urgent, urgent)
	return err
}

func validAIWorkKind(kind string) bool {
	return kind == aiwork.FactRefill || kind == aiwork.MemeRefill || kind == aiwork.FeatureTitle
}

func (s *Store) SeedAIWork(ctx context.Context, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE items SET state='rejected'
 WHERE kind='meme' AND ai_approved=1 AND state IN ('pending','approved') AND created_at<?`, now.Add(-memeStockTTL).Unix()); err != nil {
			return err
		}
		if err := seedContentAIWork(ctx, tx, now); err != nil {
			return err
		}
		return seedFeatureTitleWork(ctx, tx, now)
	})
}

type contentStock struct {
	chat       int64
	kind       string
	moderation bool
	count      int
}

func seedContentAIWork(ctx context.Context, tx *sql.Tx, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT s.chat_id,s.kind,c.moderation,
 (SELECT count(*) FROM items i WHERE i.chat_id=s.chat_id AND i.kind=s.kind AND i.ai_approved=1
   AND i.state IN ('pending','approved','reserved')
   AND (i.kind!='meme' OR i.created_at>=?)
   AND (i.kind!='fact' OR i.generation_policy='fact-v4'))
 FROM schedules s JOIN chats c USING(chat_id)
 WHERE s.enabled=1 AND c.active=1 ORDER BY s.chat_id,s.kind`, now.Add(-memeStockTTL).Unix())
	if err != nil {
		return err
	}
	defer rows.Close()
	var stocks []contentStock
	for rows.Next() {
		var current contentStock
		if err = rows.Scan(&current.chat, &current.kind, &current.moderation, &current.count); err != nil {
			return err
		}
		stocks = append(stocks, current)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, current := range stocks {
		if err = maintainContentWork(ctx, tx, current, now); err != nil {
			return err
		}
	}
	return nil
}

func maintainContentWork(ctx context.Context, tx *sql.Tx, stock contentStock, now time.Time) error {
	workKind := aiwork.FactRefill
	if stock.kind == daily.Meme {
		workKind = aiwork.MemeRefill
	}
	key := fmt.Sprintf("%s:%d", stock.kind, stock.chat)
	if stock.count >= backgroundStockTarget {
		if _, err := tx.ExecContext(ctx, `UPDATE ai_work SET state='done',claimed_until=0,updated_at=?
 WHERE work_key=? AND state!='running'`, now.Unix(), key); err != nil {
			return err
		}
		return nil
	}
	priority := aiwork.PriorityNormal
	if stock.count < backgroundStockLow {
		priority = aiwork.PriorityLowStock
	}
	return enqueueAIWorkTx(ctx, tx, workKind, stock.chat, key, priority, now)
}

type untitledIdea struct {
	id              int64
	weekday         int
	clock, zone     string
	scheduleEnabled bool
}

func seedFeatureTitleWork(ctx context.Context, tx *sql.Tx, now time.Time) error {
	ideas, err := tx.QueryContext(ctx, `SELECT f.id,COALESCE(v.weekday,-1),COALESCE(v.clock,''),COALESCE(c.zone,''),COALESCE(v.enabled,0)
 FROM feature_requests f JOIN chats c USING(chat_id)
 LEFT JOIN feature_vote_schedules v USING(chat_id)
 WHERE f.state='active' AND f.title='' ORDER BY f.id`)
	if err != nil {
		return err
	}
	defer ideas.Close()
	var untitled []untitledIdea
	for ideas.Next() {
		var idea untitledIdea
		if err = ideas.Scan(&idea.id, &idea.weekday, &idea.clock, &idea.zone, &idea.scheduleEnabled); err != nil {
			return err
		}
		untitled = append(untitled, idea)
	}
	if err = ideas.Err(); err != nil {
		return err
	}
	if err = ideas.Close(); err != nil {
		return err
	}
	for _, idea := range untitled {
		priority := aiwork.PriorityNormal
		if idea.scheduleEnabled && featureVoteWithin(now, idea.weekday, idea.clock, idea.zone, 24*time.Hour) {
			priority = aiwork.PriorityUpcoming
		}
		if err = enqueueAIWorkTx(ctx, tx, aiwork.FeatureTitle, idea.id, fmt.Sprintf("feature_title:%d", idea.id), priority, now); err != nil {
			return err
		}
	}
	return nil
}

func featureVoteWithin(now time.Time, weekday int, clock, zone string, horizon time.Duration) bool {
	slot, err := featurevote.WeeklySlot(now, weekday, clock, zone)
	if err != nil {
		return false
	}
	if slot.Before(now) {
		slot, err = featurevote.WeeklySlot(now.AddDate(0, 0, 7), weekday, clock, zone)
	}
	return err == nil && !slot.Before(now) && slot.Sub(now) <= horizon
}

func enqueueAIWorkTx(ctx context.Context, tx *sql.Tx, kind string, scopeID int64, key string, priority int, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO ai_work(kind,scope_id,work_key,state,priority,next_attempt,created_at,updated_at)
 VALUES(?,?,?,'pending',?,?,?,?)
 ON CONFLICT(work_key) DO UPDATE SET
  priority=CASE WHEN ai_work.state='done' THEN excluded.priority ELSE MAX(ai_work.priority,excluded.priority) END,
  state=CASE WHEN ai_work.state='done' THEN 'pending' ELSE ai_work.state END,
  next_attempt=CASE WHEN ai_work.state='done' THEN excluded.next_attempt ELSE ai_work.next_attempt END,
  attempts=CASE WHEN ai_work.state='done' THEN 0 ELSE ai_work.attempts END,
  last_error_code=CASE WHEN ai_work.state='done' THEN '' ELSE ai_work.last_error_code END,
  updated_at=excluded.updated_at`, kind, scopeID, key, priority, now.Unix(), now.Unix(), now.Unix())
	return err
}

func (s *Store) ClaimAIWork(ctx context.Context, now time.Time, lease time.Duration) (aiwork.Work, bool, error) {
	var work aiwork.Work
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE ai_work SET state='waiting',next_attempt=?,claimed_until=0,last_error_code='lease_expired',updated_at=?
 WHERE state='running' AND claimed_until<=?`, now.Unix(), now.Unix(), now.Unix()); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT id,kind,scope_id,work_key,state,priority,attempts,next_attempt,claimed_until,last_error_code,updated_at
 FROM ai_work WHERE state IN ('pending','waiting') AND next_attempt<=?
 ORDER BY priority DESC,next_attempt,id LIMIT 1`, now.Unix()).Scan(
			&work.ID, &work.Kind, &work.ScopeID, &work.Key, &work.State, &work.Priority, &work.Attempts,
			&work.NextAttempt, &work.ClaimedUntil, &work.LastErrorCode, &work.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE ai_work SET state='running',attempts=attempts+1,claimed_until=?,updated_at=?
 WHERE id=? AND state IN ('pending','waiting') AND next_attempt<=?`, now.Add(lease).Unix(), now.Unix(), work.ID, now.Unix())
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return err
		}
		work.State = "running"
		work.Attempts++
		work.ClaimedUntil = now.Add(lease).Unix()
		return nil
	})
	if err != nil {
		return aiwork.Work{}, false, err
	}
	return work, work.ID != 0, nil
}

func (s *Store) DeferAIWork(ctx context.Context, id int64, next time.Time, reason string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE ai_work SET state='waiting',next_attempt=?,claimed_until=0,last_error_code=?,updated_at=? WHERE id=? AND state='running'`, next.Unix(), reason, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return daily.ErrConflict
	}
	return nil
}

func (s *Store) CompleteAIWork(ctx context.Context, id int64, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE ai_work SET state='done',next_attempt=0,claimed_until=0,last_error_code='',updated_at=? WHERE id=? AND state='running'`, now.Unix(), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return daily.ErrConflict
	}
	return nil
}

func (s *Store) AIStock(ctx context.Context, chat int64, kind string, now time.Time) (int, error) {
	if !daily.ValidKind(kind) {
		return 0, errors.New("invalid stock kind")
	}
	cutoff := int64(0)
	if kind == daily.Meme {
		cutoff = now.Add(-memeStockTTL).Unix()
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM items
 WHERE chat_id=? AND kind=? AND ai_approved=1 AND state IN ('pending','approved','reserved')
 AND (?=0 OR created_at>=?)
 AND (kind!='fact' OR generation_policy='fact-v4')`, chat, kind, cutoff, cutoff).Scan(&count)
	return count, err
}

func (s *Store) SaveAIItems(ctx context.Context, chat int64, kind string, candidates []daily.Item, now time.Time) (int, error) {
	inserted := 0
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var moderation bool
		if err := tx.QueryRowContext(ctx, "SELECT moderation FROM chats WHERE chat_id=? AND active=1", chat).Scan(&moderation); err != nil {
			return err
		}
		state := "approved"
		if moderation {
			state = "pending"
		}
		for _, candidate := range candidates {
			candidate.ChatID = chat
			candidate.Kind = kind
			if err := candidate.Validate(); err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO items(chat_id,kind,text,source,image,content_key,author_id,state,created_at,ai_approved,source_evidence,ai_provider,generation_policy)
 VALUES(?,?,?,?,?,?,0,?,?,1,?,?,?)`, chat, kind, candidate.Text, candidate.Source, candidate.Image, candidate.Key, state, now.Unix(), candidate.SourceEvidence, candidate.AIProvider, candidate.GenerationPolicy)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			inserted += int(n)
		}
		return nil
	})
	return inserted, err
}

func (s *Store) FeatureForTitle(ctx context.Context, id int64) (featurevote.Idea, error) {
	return scanFeatureIdea(s.db.QueryRowContext(ctx, "SELECT "+featureIdeaColumns+" FROM feature_requests WHERE id=?", id))
}

func (s *Store) PreviewAIItem(ctx context.Context, chat int64, kind string, now time.Time) (daily.Item, bool, error) {
	if !daily.ValidKind(kind) {
		return daily.Item{}, false, errors.New("invalid preview kind")
	}
	cutoff := int64(0)
	if kind == daily.Meme {
		cutoff = now.Add(-memeStockTTL).Unix()
	}
	item, err := scanItem(s.db.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM items i
 WHERE i.chat_id=? AND i.kind=? AND i.state='approved'
 AND EXISTS (SELECT 1 FROM chats c WHERE c.chat_id=i.chat_id AND ((c.moderation=1 AND i.approved_by IS NOT NULL) OR (c.moderation=0 AND i.ai_approved=1)))
 AND (i.ai_approved=0 OR ?=0 OR i.created_at>=?)
 AND (i.kind!='fact' OR i.ai_approved=0 OR i.generation_policy='fact-v4')
 ORDER BY i.id LIMIT 1`, chat, kind, cutoff, cutoff))
	if errors.Is(err, sql.ErrNoRows) {
		return daily.Item{}, false, nil
	}
	return item, err == nil, err
}

func (s *Store) RequestAIRefill(ctx context.Context, chat int64, kind string, now time.Time) error {
	if kind == "titles" {
		return s.requestTitleRefill(ctx, chat, now)
	}
	if !daily.ValidKind(kind) {
		return errors.New("invalid refill kind")
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM chats WHERE chat_id=? AND active=1)", chat).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errors.New("chat is unavailable")
	}
	workKind := aiwork.FactRefill
	if kind == daily.Meme {
		workKind = aiwork.MemeRefill
	}
	return s.EnqueueAIWork(ctx, workKind, chat, fmt.Sprintf("%s:%d", kind, chat), aiwork.PriorityUrgent, now, true)
}

func (s *Store) requestTitleRefill(ctx context.Context, chat int64, now time.Time) error {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM feature_requests WHERE chat_id=? AND state='active' AND title='' ORDER BY id", chat)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.EnqueueAIWork(ctx, aiwork.FeatureTitle, id, fmt.Sprintf("feature_title:%d", id), aiwork.PriorityUrgent, now, true); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) AIStocks(ctx context.Context, chat int64, now time.Time) ([]daily.Stock, error) {
	stocks := make([]daily.Stock, 0, 2)
	for _, kind := range []string{daily.Fact, daily.Meme} {
		count, err := s.AIStock(ctx, chat, kind, now)
		if err != nil {
			return nil, err
		}
		workKind := aiwork.FactRefill
		if kind == daily.Meme {
			workKind = aiwork.MemeRefill
		}
		stock := daily.Stock{Kind: kind, Count: count, Target: backgroundStockTarget}
		err = s.db.QueryRowContext(ctx, `SELECT next_attempt,last_error_code FROM ai_work WHERE kind=? AND scope_id=?`, workKind, chat).Scan(&stock.NextAttempt, &stock.LastError)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		stocks = append(stocks, stock)
	}
	return stocks, nil
}
