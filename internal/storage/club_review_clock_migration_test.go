package storage

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func TestReviewClockUpgradePreservesEvidenceAndUserDecisions(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	m, err := s.migrator()
	must(t, err)
	_, err = m.DownTo(ctx, 20)
	must(t, err)
	must(t, s.EnsureChat(ctx, -1))
	must(t, s.EnsureChat(ctx, -2))
	_, err = s.db.ExecContext(ctx, "INSERT INTO club_profile_settings(chat_id,auto_review) VALUES(-2,0)")
	must(t, err)
	_, err = s.db.ExecContext(ctx, "INSERT INTO club_films(tmdb_id,title,source,fetched_at) VALUES(603,'Матрица','tmdb',?)", testNow.Unix())
	must(t, err)
	plan, err := json.Marshal(clubprofile.DefaultReviewPlan())
	must(t, err)
	cases := []struct {
		state, reason, want, watchState string
		chat                            int64
		published                       bool
	}{
		{"queued", "ready", "queued", "active", -1, false},
		{"expired", "start_deadline", "queued", "active", -1, false},
		{"partial", "deadline", "queued", "active", -1, false},
		{"researching", "awaiting_results", "researching", "active", -1, true},
		{"blocked", "delivery_or_result_unknown", "blocked", "active", -1, false},
		{"partial", "question_budget", "partial", "active", -1, false},
		{"completed_sufficient", "coverage_met", "completed_sufficient", "active", -1, true},
		{"cancelled", "pause", "cancelled", "active", -1, false},
		{"expired", "start_deadline", "expired", "annulled", -1, false},
		{"partial", "deadline", "partial", "active", -2, false},
		{"partial", "deadline", "partial", "active", -1, true},
		{"expired", "start_deadline", "expired", "active", -1, false},
	}
	old := testNow.AddDate(0, 0, -90)
	oldFinish := old.AddDate(0, 0, 28).Unix()
	for i, tc := range cases {
		id := i + 1
		_, err = s.db.ExecContext(ctx, `INSERT INTO club_watches(id,chat_id,tmdb_id,watched_date,date_precision,zone,mode,state,origin,created_at)
 VALUES(?,?,603,?,'day','UTC','fresh',?,?,?)`, id, tc.chat, old.AddDate(0, 0, i).Format(time.DateOnly), tc.watchState, fmt.Sprint(id), old.Unix())
		must(t, err)
		_, err = s.db.ExecContext(ctx, `INSERT INTO club_review_sessions(id,chat_id,watch_id,plan_json,state,reason,start_before,finish_before,created_at)
 VALUES(?,?,?,?,?,?,?,?,?)`, id, tc.chat, id, plan, tc.state, tc.reason, old.AddDate(0, 0, 14).Unix(), oldFinish, old.Unix())
		must(t, err)
		_, err = s.db.ExecContext(ctx, `INSERT INTO club_watch_drafts(chat_id,operation_id,input_json,state,watch_id,expires_at,next_attempt,version,rendered)
 VALUES(?,?,'{}','saved',?,0,0,1,1)`, tc.chat, id, id)
		must(t, err)
		if tc.published {
			for j := range 2 {
				at := testNow.Add(time.Duration(j) * time.Hour).Unix()
				_, err = s.db.ExecContext(ctx, `INSERT INTO club_profile_polls(chat_id,topic,session_id,occurrence,state,question_json,message_id,opened_at,slot_at,closes_at,duration,result_json)
 VALUES(?,'post_watch',?,?,'completed','{}',?,?,?,?,86400,'{"evidence":"unchanged"}')`, tc.chat, id, fmt.Sprintf("%d:%d", id, j), id*10+j, at, at, at+86400)
				must(t, err)
			}
		}
		if tc.state == "blocked" {
			_, err = s.db.ExecContext(ctx, `INSERT INTO club_profile_polls(chat_id,topic,session_id,occurrence,state,question_json,slot_at,closes_at,duration,delivery_counted)
 VALUES(?,'post_watch',?,'unknown','unknown','{}',0,0,86400,1)`, tc.chat, id)
			must(t, err)
		}
	}
	// A pause/date edit performed after expiry can leave its old terminal reason.
	_, err = s.db.ExecContext(ctx, "UPDATE club_watches SET revision=2 WHERE id=?", len(cases))
	must(t, err)
	_, err = m.Up(ctx)
	must(t, err)
	sessions, err := s.ProfileSessions(ctx, 0)
	must(t, err)
	if len(sessions) != len(cases) {
		t.Fatal("session count changed", len(sessions))
	}
	for i, tc := range cases {
		got := sessions[i]
		if got.State != tc.want {
			t.Fatalf("case %d state = %s, want %s", i, got.State, tc.want)
		}
		active := got.State == "queued" || got.State == "researching" || got.State == "blocked"
		wantDeadline := time.Unix(oldFinish, 0)
		if active {
			wantDeadline = time.Time{}
			if tc.published {
				wantDeadline = testNow.Add(clubprofile.ReviewWindow)
			}
		}
		if !got.FinishBefore.Equal(wantDeadline) {
			t.Fatalf("case %d deadline = %v, want %v", i, got.FinishBefore, wantDeadline)
		}
		var version int
		must(t, s.db.QueryRowContext(ctx, "SELECT version FROM club_watch_drafts WHERE watch_id=?", got.WatchID).Scan(&version))
		if active && version <= 1 {
			t.Fatal("old watch card not refreshed", got.WatchID)
		}
	}
	var evidence, unknown int
	must(t, s.db.QueryRowContext(ctx, `SELECT count(*) FROM club_profile_polls WHERE result_json='{"evidence":"unchanged"}'`).Scan(&evidence))
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM club_profile_polls WHERE state='unknown' AND delivery_counted=1").Scan(&unknown))
	if evidence != 6 || unknown != 1 {
		t.Fatal("migration changed poll evidence", evidence, unknown)
	}
}
