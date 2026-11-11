package clubprofile

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func statisticsPoll(t *testing.T, id, session int64, q Question, counts []int) Poll {
	t.Helper()
	n := 0
	for _, v := range counts {
		n += v
	}
	return Poll{ID: id, ChatID: -1, SessionID: session, State: Completed, TelegramID: "poll", Question: q, ClosesAt: time.Unix(id, 0), Result: &PollResult{PollID: "poll", Counts: counts, Total: new(n), Closed: true, Source: SourceStop, ReceivedAt: time.Unix(id, 0)}}
}
func statisticsFixture(t *testing.T) StatisticsInput {
	t.Helper()
	q, err := ReviewQuestion(Overall, "Она")
	if err != nil {
		t.Fatal(err)
	}
	return StatisticsInput{ChatID: -1, Zone: "UTC", Watches: []WatchEvent{{ID: 1, ChatID: -1, TMDBID: 603, State: "active", Revision: 1, Date: "2026-01-01", Film: Film{Title: "Она", Year: 2013}}}, Sessions: []ReviewSession{{ID: 1, ChatID: -1, WatchID: 1}}, Polls: []Poll{statisticsPoll(t, 1, 1, q, []int{2, 1, 0, 2, 0, 1, 0})}}
}
func TestStatisticsCountsClaimsAndIsolation(t *testing.T) {
	in := statisticsFixture(t)
	now := time.Unix(100, 0)
	duplicate := in.Polls[0]
	duplicate.ID = 2
	duplicate.ClosesAt = time.Unix(2, 0)
	in.Polls = append(in.Polls, duplicate)
	foreign := duplicate
	foreign.ID = 3
	foreign.ChatID = -2
	in.Polls = append(in.Polls, foreign)
	open := duplicate
	open.ID = 4
	open.State = Open
	in.Polls = append(in.Polls, open)
	malformed := duplicate
	malformed.ID = 5
	bad := *duplicate.Result
	bad.Counts = []int{99}
	malformed.Result = &bad
	in.Polls = append(in.Polls, malformed)
	s := BuildStatistics(in, StatisticsPeriod{}, now)
	if s.Polls != 2 || s.Answers != 12 || s.Invalid != 1 || len(s.Claims) != 1 || s.Claims[0].PollID != 1 {
		t.Fatalf("wrong aggregate: %+v", s)
	}
	if !strings.Contains(s.Claims[0].Text, "Мнения разделились") || !strings.Contains(s.Claims[0].Text, "оценок: 5") {
		t.Fatal(s.Claims[0].Text)
	}
	slices.Reverse(in.Polls)
	again := BuildStatistics(in, StatisticsPeriod{}, now)
	if again.Claims[0].ID != s.Claims[0].ID {
		t.Fatal("order-dependent claim")
	}
	in.ChatID = 0
	if len(BuildStatistics(in, StatisticsPeriod{}, now).Claims) != 0 {
		t.Fatal("global scope allowed")
	}
}
func TestStatisticsResearchCanonicalAndRevision(t *testing.T) {
	qs := researchQuestions(t)
	plan, err := ResearchReviewPlan("bundle", qs)
	if err != nil {
		t.Fatal(err)
	}
	in := statisticsFixture(t)
	in.Sessions[0].Plan = plan
	in.Polls = []Poll{statisticsPoll(t, 1, 1, qs[0], []int{0, 0, 3, 0, 1, 0, 0}), statisticsPoll(t, 2, 1, qs[2], []int{0, 0, 0, 0, 2, 0, 0}), statisticsPoll(t, 3, 1, qs[2], []int{0, 0, 0, 0, 2, 0, 0})}
	s := BuildStatistics(in, StatisticsPeriod{}, time.Unix(100, 0))
	if len(s.Claims) != 1 || s.Claims[0].PollID != 1 || s.Research.Provisional != 2 {
		t.Fatalf("%+v", s)
	}
	in.Watches[0].Revision++
	if len(BuildStatistics(in, StatisticsPeriod{}, time.Unix(100, 0)).Claims) != 0 {
		t.Fatal("stale revision entered report")
	}
}
func TestStatisticsAsOfAndPeriod(t *testing.T) {
	in := statisticsFixture(t)
	s := BuildStatistics(in, StatisticsPeriod{Start: time.Unix(2, 0)}, time.Unix(100, 0))
	if s.Polls != 0 || len(s.Claims) != 0 {
		t.Fatal(s)
	}
	in.Polls[0].Result.ReceivedAt = time.Unix(200, 0)
	if BuildStatistics(in, StatisticsPeriod{}, time.Unix(100, 0)).Polls != 0 {
		t.Fatal("future knowledge")
	}
}
func TestStatisticsDigestPeriodAndWeekIdentity(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Novosibirsk")
	if err != nil {
		t.Fatal(err)
	}
	slot := time.Date(2026, 11, 13, 9, 0, 0, 0, loc)
	p := DigestPeriod(slot)
	if p.Start.Format("2006-01-02 15:04") != "2026-11-06 00:00" || p.End.Format("2006-01-02 15:04") != "2026-11-13 00:00" {
		t.Fatal(p)
	}
	if DigestWeekKey(slot) != DigestWeekKey(slot.AddDate(0, 0, 1)) {
		t.Fatal("schedule change duplicates week")
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	dst := DigestPeriod(time.Date(2026, 10, 30, 9, 0, 0, 0, berlin))
	if dst.End.Sub(dst.Start) != 169*time.Hour {
		t.Fatal("DST reduced to fixed hours", dst)
	}
}
func TestStatisticsChoiceTiesAndCaptionIndependentFingerprint(t *testing.T) {
	q := Question{Topic: Reference, Intent: Interest, Version: QuestionV1, Scale: "film-choice", Anonymous: true, Text: "Что ближе?", Options: []Option{{Key: "a", Text: "А", Substantive: true}, {Key: "b", Text: "Б", Substantive: true}, {Key: "c", Text: "В", Substantive: true}}}
	p := statisticsPoll(t, 1, 0, q, []int{1, 1, 1})
	in := StatisticsInput{ChatID: -1, Polls: []Poll{p}}
	first := BuildStatistics(in, StatisticsPeriod{}, time.Unix(100, 0)).Claims[0]
	if !strings.Contains(first.Text, "ничья") || strings.Contains(first.Text, "Лидер") {
		t.Fatal(first.Text)
	}
	in.Polls[0].Question.Description = "Updated UI caption"
	second := BuildStatistics(in, StatisticsPeriod{}, time.Unix(100, 0)).Claims[0]
	if first.ID != second.ID {
		t.Fatal("caption creates new analytical fact")
	}
}
