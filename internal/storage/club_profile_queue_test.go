package storage

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

type queueCatalog struct{}

func (queueCatalog) SearchFilms(context.Context, string) ([]clubprofile.Film, error) { return nil, nil }
func (queueCatalog) FilmIdentity(context.Context, int64) (clubprofile.Film, error) {
	return clubprofile.Film{}, nil
}
func (queueCatalog) FilmDetails(context.Context, int64) (clubprofile.Film, error) {
	return clubprofile.Film{}, nil
}

type queuePublisher struct{ calls int }

func (e *queuePublisher) PublishWatchCard(context.Context, clubprofile.WatchDraft) error {
	e.calls++
	return nil
}

type queueDeleter struct{ calls int }

func (d *queueDeleter) DeleteProfilePoll(context.Context, clubprofile.CleanupJob) error {
	d.calls++
	return nil
}
func TestProfileQueuesSkipRemovedChatBeforeLimit(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	must(t, s.EnsureChat(ctx, -2))
	for i := 1; i <= 6; i++ {
		chat := int64(-1)
		if i == 6 {
			chat = -2
		}
		d, e := s.CreateWatchDraft(ctx, int64(800+i), chat, clubprofile.WatchInput{Query: "film"}, testNow)
		must(t, e)
		_, e = s.ClaimWatchMessage(ctx, chat, d.ID, testNow)
		must(t, e)
		must(t, s.BindWatchMessage(ctx, chat, d.ID, i))
		_, e = s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET state='choice' WHERE id=?", d.ID)
		must(t, e)
	}
	svc, e := clubprofile.NewWatchService(s, queueCatalog{}, map[int64]bool{-2: true}, slog.Default(), func() time.Time { return testNow })
	must(t, e)
	ed := &queuePublisher{}
	for range 3 {
		must(t, svc.Tick(ctx, ed))
	}
	if ed.calls != 1 {
		t.Fatalf("allowed card was blocked or duplicated: %d", ed.calls)
	}

	for i := 1; i <= 21; i++ {
		chat := int64(-1)
		if i == 21 {
			chat = -2
		}
		_, e = s.db.ExecContext(ctx, `INSERT INTO club_profile_polls(id,chat_id,topic,occurrence,state,question_json,message_id,slot_at,closes_at,duration,result_json) VALUES(?,?,'genre',?,'completed','{}',?,0,1,60,'{}')`, i, chat, fmt.Sprint(i), i)
		must(t, e)
		_, e = s.db.ExecContext(ctx, `INSERT INTO club_poll_cleanup(poll_id,chat_id,message_id,state,next_attempt,deadline) VALUES(?,?,?,'pending',0,1)`, i, chat, i)
		must(t, e)
	}
	del := &queueDeleter{}
	worker := clubprofile.CleanupWorker{Store: s, Transport: del, Allowed: map[int64]bool{-2: true}, Log: slog.Default(), Now: func() time.Time { return testNow }}
	for range 3 {
		must(t, worker.Tick(ctx))
	}
	var state string
	must(t, s.db.QueryRowContext(ctx, "SELECT state FROM club_poll_cleanup WHERE poll_id=21").Scan(&state))
	if state != "expired" {
		t.Fatalf("allowed cleanup was blocked: %s", state)
	}
	if del.calls != 0 {
		t.Fatal("expired poll deleted")
	}
	var removed string
	must(t, s.db.QueryRowContext(ctx, "SELECT state FROM club_poll_cleanup WHERE poll_id=1").Scan(&removed))
	if removed != "pending" {
		t.Fatal("removed chat was processed")
	}
	_, e = s.db.ExecContext(ctx, "UPDATE chats SET active=0 WHERE chat_id=-2")
	must(t, e)
	_, e = s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET rendered=0,state='pending' WHERE chat_id=-2")
	must(t, e)
	cards, e := s.WatchCards(ctx, -2, testNow)
	must(t, e)
	if len(cards) != 0 {
		t.Fatal("suspended chat eligible")
	}
	drafts, e := s.DueWatchDrafts(ctx, -2, testNow)
	must(t, e)
	if len(drafts) != 0 {
		t.Fatal("suspended chat eligible for catalog requests")
	}
}
