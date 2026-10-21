package storage

import (
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func TestReviewQueueDoesNotExpireWhileWaiting(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	addProfileWatch(t, s, -1, 1100, testNow.Format(time.DateOnly), testNow)
	addProfileWatch(t, s, -1, 1101, testNow.AddDate(0, 0, -1).Format(time.DateOnly), testNow)
	// A paused schedule or a long queue must not consume a film's discussion time.
	later := testNow.AddDate(0, 0, 90)
	must(t, s.AdvanceProfileReviews(ctx, later))
	sessions, err := s.ProfileSessions(ctx, -1)
	must(t, err)
	for _, session := range sessions {
		if session.State != "queued" || !session.FinishBefore.IsZero() {
			t.Fatalf("waiting film lost its discussion window: %+v", session)
		}
		if d := clubprofile.ReviewProgress(session, nil, later); len(d.Next) != 2 {
			t.Fatal("waiting film cannot start", d)
		}
	}
}

func TestReviewClockStartsAtFirstPublishedQuestion(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	addProfileWatch(t, s, -1, 1120, testNow.Format(time.DateOnly), testNow)
	sessions, err := s.ProfileSessions(ctx, -1)
	must(t, err)
	session := sessions[0]
	_, err = s.db.ExecContext(ctx, "UPDATE club_review_sessions SET state='researching' WHERE id=?", session.ID)
	must(t, err)
	first := testNow.AddDate(0, 0, 5)
	for i, aspect := range []clubprofile.Aspect{clubprofile.Overall, clubprofile.Pace} {
		q, e := clubprofile.ReviewQuestion(aspect, "Матрица")
		must(t, e)
		at := first.Add(time.Duration(i) * time.Minute)
		id, e := s.PlanProfilePoll(ctx, new(int64(1130+i)), clubprofile.Poll{ChatID: -1, SessionID: session.ID, Question: q, SlotAt: at, Duration: clubprofile.DefaultDuration})
		must(t, e)
		ok, e := s.ClaimProfilePoll(ctx, id, clubprofile.Planned, clubprofile.Creating)
		must(t, e)
		if !ok {
			t.Fatal("question not claimed")
		}
		must(t, s.OpenProfilePoll(ctx, id, string(aspect), i+1, at))
		sessions, e = s.ProfileSessions(ctx, -1)
		must(t, e)
		if want := first.Add(28 * 24 * time.Hour); !sessions[0].FinishBefore.Equal(want) {
			t.Fatalf("deadline = %v, want %v after publication %d", sessions[0].FinishBefore, want, i+1)
		}
	}
	deadline := first.Add(28 * 24 * time.Hour)
	must(t, s.AdvanceProfileReviews(ctx, deadline.Add(-time.Second)))
	sessions, err = s.ProfileSessions(ctx, -1)
	must(t, err)
	if sessions[0].State != "researching" {
		t.Fatal("discussion stopped early", sessions[0])
	}
	must(t, s.AdvanceProfileReviews(ctx, deadline))
	sessions, err = s.ProfileSessions(ctx, -1)
	must(t, err)
	if sessions[0].State != "partial" || sessions[0].Reason != "deadline" {
		t.Fatal("discussion did not stop at its own deadline", sessions[0])
	}
}

func TestReviewClockDoesNotStartOnUnconfirmedDelivery(t *testing.T) {
	for _, state := range []clubprofile.PollState{clubprofile.Planned, clubprofile.Cancelled, clubprofile.Unknown} {
		t.Run(string(state), func(t *testing.T) {
			s := testStore(t)
			ctx := t.Context()
			must(t, s.EnsureChat(ctx, -1))
			addProfileWatch(t, s, -1, 1150, testNow.Format(time.DateOnly), testNow)
			sessions, err := s.ProfileSessions(ctx, -1)
			must(t, err)
			session := sessions[0]
			_, err = s.db.ExecContext(ctx, "UPDATE club_review_sessions SET state='researching' WHERE id=?", session.ID)
			must(t, err)
			q, err := clubprofile.ReviewQuestion(clubprofile.Overall, "Матрица")
			must(t, err)
			id, err := s.PlanProfilePoll(ctx, new(int64(1151)), clubprofile.Poll{ChatID: -1, SessionID: session.ID, Question: q, SlotAt: testNow, Duration: clubprofile.DefaultDuration})
			must(t, err)
			if state == clubprofile.Cancelled {
				must(t, s.DeferProfilePoll(ctx, id, clubprofile.Planned, state, testNow, "slot_expired"))
			} else {
				ok, e := s.ClaimProfilePoll(ctx, id, clubprofile.Planned, clubprofile.Creating)
				must(t, e)
				if !ok {
					t.Fatal("question not claimed")
				}
				must(t, s.DeferProfilePoll(ctx, id, clubprofile.Creating, state, testNow.Add(time.Minute), "transport"))
			}
			must(t, s.AdvanceProfileReviews(ctx, testNow.AddDate(0, 0, 90)))
			sessions, err = s.ProfileSessions(ctx, -1)
			must(t, err)
			if !sessions[0].FinishBefore.IsZero() || sessions[0].State == "partial" || sessions[0].State == "expired" {
				t.Fatal("unconfirmed publication consumed discussion time", sessions[0])
			}
			if state == clubprofile.Unknown && sessions[0].State != "blocked" {
				t.Fatal("unknown publication must still block retries", sessions[0])
			}
		})
	}
}

func TestReviewClockAndReceiptCommitTogether(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	addProfileWatch(t, s, -1, 1160, testNow.Format(time.DateOnly), testNow)
	sessions, err := s.ProfileSessions(ctx, -1)
	must(t, err)
	session := sessions[0]
	_, err = s.db.ExecContext(ctx, "UPDATE club_review_sessions SET state='researching' WHERE id=?", session.ID)
	must(t, err)
	q, err := clubprofile.ReviewQuestion(clubprofile.Overall, "Матрица")
	must(t, err)
	id, err := s.PlanProfilePoll(ctx, new(int64(1161)), clubprofile.Poll{ChatID: -1, SessionID: session.ID, Question: q, SlotAt: testNow, Duration: clubprofile.DefaultDuration})
	must(t, err)
	ok, err := s.ClaimProfilePoll(ctx, id, clubprofile.Planned, clubprofile.Creating)
	must(t, err)
	if !ok {
		t.Fatal("question not claimed")
	}
	_, err = s.db.ExecContext(ctx, `CREATE TRIGGER fail_review_clock BEFORE UPDATE OF finish_before ON club_review_sessions BEGIN SELECT RAISE(ABORT,'clock write failed'); END`)
	must(t, err)
	if err = s.OpenProfilePoll(ctx, id, "atomic", 300, testNow); err == nil {
		t.Fatal("clock failure ignored")
	}
	polls, err := s.ProfilePolls(ctx, -1, "")
	must(t, err)
	if polls[0].State != clubprofile.Creating || polls[0].MessageID != 0 || polls[0].TelegramID != "" {
		t.Fatal("partial receipt saved", polls[0])
	}
	_, err = s.db.ExecContext(ctx, "DROP TRIGGER fail_review_clock")
	must(t, err)
	must(t, s.OpenProfilePoll(ctx, id, "atomic", 300, testNow))
	sessions, err = s.ProfileSessions(ctx, -1)
	must(t, err)
	if !sessions[0].FinishBefore.Equal(testNow.Add(clubprofile.ReviewWindow)) {
		t.Fatal("clock not committed", sessions[0])
	}
}
