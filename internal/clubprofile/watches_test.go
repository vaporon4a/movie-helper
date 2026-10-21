package clubprofile

import (
	"testing"
	"time"
)

func TestWatchInputIdentityAndCalendarDates(t *testing.T) {
	now := time.Date(2026, 10, 21, 20, 0, 0, 0, time.UTC)
	in, err := ParseWatchInput("https://www.themoviedb.org/movie/603-the-matrix", "Asia/Novosibirsk", now)
	if err != nil || in.TMDBID != 603 || in.Date != "2026-10-22" {
		t.Fatal(in, err)
	}
	for _, raw := range []string{"https://evil.example/movie/603", "https://themoviedb.org.evil/movie/603", "https://user@themoviedb.org/movie/603", "https://themoviedb.org:443/movie/603", "https://themoviedb.org/tv/603", "http://themoviedb.org/movie/603", "https://themoviedb.org/movie/-1", "Матрица | 2026-10-23"} {
		if _, err = ParseWatchInput(raw, "UTC", now); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	for _, date := range []string{"unknown", "2020", "2020-04", "2020-04-02"} {
		in, err = ParseWatchInput("Матрица | "+date, "UTC", now)
		if err != nil {
			t.Fatal(err)
		}
		if FreshForReview(in, now) {
			t.Fatal("historical survey")
		}
	}
}

func TestReviewStopsWithoutCallingPartialComplete(t *testing.T) {
	now := time.Now()
	s := ReviewSession{State: "researching", Plan: DefaultReviewPlan(), FinishBefore: now.Add(28 * 24 * time.Hour)}
	polls := make([]Poll, 0, 4)
	for i, a := range []Aspect{Overall, Pace, Engagement, Characters} {
		q, _ := ReviewQuestion(a, "X")
		counts := make([]int, len(q.Options))
		counts[len(counts)-1] = 3
		polls = append(polls, Poll{BatchID: int64(i/2 + 1), MessageID: i + 1, State: Completed, Question: q, Result: &PollResult{PollID: "p", Counts: counts, Total: new(3), Closed: true, Source: SourceStop, ReceivedAt: now}})
	}
	d := ReviewProgress(s, polls, now)
	if d.State != "partial" || d.Reason != "two_empty_batches" || d.Coverage.Sufficient {
		t.Fatal(d)
	}
	d = ReviewProgress(s, nil, s.FinishBefore)
	if d.State != "partial" || d.Reason != "deadline" {
		t.Fatal(d)
	}
}

func TestProfileProjectionKeepsFilmWeightsAndUnknownSeparate(t *testing.T) {
	w := []WatchEvent{{ID: 1, TMDBID: 603, State: "active"}, {ID: 2, TMDBID: 603, State: "active"}, {ID: 3, TMDBID: 604, State: "active"}, {ID: 4, TMDBID: 605, State: "annulled"}}
	s := []ReviewSession{{ID: 1, WatchID: 1}, {ID: 2, WatchID: 2}, {ID: 3, WatchID: 3}, {ID: 4, WatchID: 4}}
	q, _ := ReviewQuestion(Visuals, "X")
	now := time.Now()
	polls := make([]Poll, 0, 4)
	for i, counts := range [][]int{{0, 0, 100, 20, 0}, {0, 0, 10, 0, 0}, {2, 0, 0, 0, 0}, {0, 0, 99, 0, 0}} {
		total := 0
		for _, n := range counts {
			total += n
		}
		polls = append(polls, Poll{SessionID: int64(i + 1), State: Completed, Question: q, Result: &PollResult{PollID: "p", Counts: counts, Total: &total, Closed: true, Source: SourceStop, ReceivedAt: now}})
	}
	p := ProjectProfile(w, s, polls)
	if p.Films != 2 || p.Viewings != 3 || len(p.Aspects) != 1 || p.Aspects[0].Shares["visuals:3"] != 0.5 || p.Aspects[0].Answers != 112 {
		t.Fatal(p)
	}
}
