package storage

import (
	"fmt"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"testing"
	"time"
)

func TestMemePolicyUpgradePreservesUnknownSendsAndManualItems(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	m, err := s.migrator()
	must(t, err)
	_, err = m.DownTo(ctx, 16)
	must(t, err)
	must(t, s.EnsureChat(ctx, -1))
	states := []string{"ready", "retry", "sending", "unknown", "sent"}
	for n, state := range states {
		itemState := "reserved"
		if state == "sent" {
			itemState = "sent"
		}
		_, err = s.db.ExecContext(ctx, `INSERT INTO items(id,chat_id,kind,text,image,content_key,author_id,state,created_at,ai_approved,generation_policy)
 VALUES(?,-1,'meme','','https://i.redd.it/a.png',?,0,?,0,1,'')`, n+1, fmt.Sprint(n), itemState)
		must(t, err)
		_, err = s.db.ExecContext(ctx, `INSERT INTO deliveries(chat_id,kind,local_date,item_id,deadline,state,fetch_attempts) VALUES(-1,'meme',?,?,9999999999,?,3)`, fmt.Sprint(n), n+1, state)
		must(t, err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO items(chat_id,kind,text,image,content_key,author_id,state,created_at,ai_approved) VALUES(-1,'meme','','file-id','manual',42,'approved',0,0)`)
	must(t, err)
	_, err = s.db.ExecContext(ctx, `INSERT INTO ai_work(kind,scope_id,work_key,state,priority,next_attempt,created_at,updated_at) VALUES('meme_refill',-1,'meme:-1','done',20,0,0,0)`)
	must(t, err)
	_, err = m.Up(ctx)
	must(t, err)
	for n, state := range states {
		var actual, itemState string
		var item, attempts int
		must(t, s.db.QueryRowContext(ctx, `SELECT d.state,COALESCE(d.item_id,0),d.fetch_attempts,i.state FROM deliveries d LEFT JOIN items i ON i.id=? WHERE d.local_date=?`, n+1, fmt.Sprint(n)).Scan(&actual, &item, &attempts, &itemState))
		if state == "ready" || state == "retry" {
			if actual != "preparing" || item != 0 || attempts != 0 || itemState != "rejected" {
				t.Fatal(state, actual, item, attempts, itemState)
			}
		} else if actual != state || item != n+1 || attempts != 3 || itemState == "rejected" {
			t.Fatal("uncertain send modified", state, actual, item, attempts, itemState)
		}
	}
	var manual, work string
	must(t, s.db.QueryRowContext(ctx, `SELECT state FROM items WHERE content_key='manual'`).Scan(&manual))
	must(t, s.db.QueryRowContext(ctx, `SELECT state FROM ai_work WHERE work_key='meme:-1'`).Scan(&work))
	if manual != "approved" || work != "pending" {
		t.Fatal(manual, work)
	}
}
func TestMemePolicyGuardsStockPreviewAndClaim(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	must(t, s.SetZone(ctx, 1, -1, "UTC", testNow))
	must(t, s.SetSchedule(ctx, 2, -1, daily.Meme, "09:00", true, testNow))
	old := daily.Item{Kind: daily.Meme, Image: "https://i.redd.it/a.png", Key: "old"}
	if _, err := s.SaveAIItems(ctx, -1, daily.Meme, []daily.Item{old}, testNow); err == nil {
		t.Fatal("unversioned refill accepted")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO items(chat_id,kind,text,image,content_key,author_id,state,created_at,ai_approved) VALUES(-1,'meme','','https://i.redd.it/a.png','old',0,'approved',?,1)`, testNow.Unix())
	must(t, err)
	count, err := s.AIStock(ctx, -1, daily.Meme, testNow)
	must(t, err)
	if count != 0 {
		t.Fatal(count)
	}
	_, found, err := s.PreviewAIItem(ctx, -1, daily.Meme, testNow)
	must(t, err)
	if found {
		t.Fatal("old meme previewed")
	}
	found, err = s.HasApproved(ctx, -1, daily.Meme, testNow)
	must(t, err)
	if found {
		t.Fatal("old meme reserved")
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO deliveries(chat_id,kind,local_date,item_id,deadline,state,slot_at) SELECT -1,'meme','test',id,?,'ready',? FROM items WHERE content_key='old'`, testNow.Add(time.Hour).Unix(), testNow.Add(time.Second).Unix())
	must(t, err)
	var delivery int64
	must(t, s.db.QueryRowContext(ctx, `SELECT id FROM deliveries WHERE local_date='test'`).Scan(&delivery))
	claimed, err := s.Claim(ctx, delivery, testNow)
	must(t, err)
	if claimed {
		t.Fatal("old meme claimed")
	}
}
func TestReserveAssessmentPersistsWithoutBecomingStock(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	audit := `{"policy":"meme-v4","decision":"reserve","assessment":{"humor":3,"clarity":4}}`
	must(t, s.RejectMemeReview(ctx, "gemini:model:meme-v4", "source", "reserve", audit, testNow))
	var decision, stored string
	must(t, s.db.QueryRowContext(ctx, `SELECT decision,assessment FROM meme_rejections WHERE source_key='source'`).Scan(&decision, &stored))
	if decision != "reserve" || stored != audit {
		t.Fatal(decision, stored)
	}
	rejected, err := s.MemeRejected(ctx, "gemini:model:meme-v4", "source", testNow)
	must(t, err)
	if !rejected {
		t.Fatal("reserve was retried immediately")
	}
	count, err := s.AIStock(ctx, -1, daily.Meme, testNow)
	must(t, err)
	if count != 0 {
		t.Fatal("reserve counted as publishable", count)
	}
	rejected, err = s.MemeRejected(ctx, "gemini:model:meme-v4", "source", testNow.Add(24*time.Hour))
	must(t, err)
	if rejected {
		t.Fatal("expired decision remained cached")
	}
}
