package storage

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/filmresearch"
)

func researchBundle(t *testing.T, s *Store) filmresearch.Bundle {
	t.Helper()
	must(t, s.EnsureChat(t.Context(), -1))
	draft := addProfileWatch(t, s, -1, 1001, testNow.Format(time.DateOnly), testNow)
	sessions, err := s.ProfileSessions(t.Context(), -1)
	must(t, err)
	source := filmresearch.SourceSnapshot{Version: filmresearch.SourceV1, TMDBID: 603, Kind: "plot", URL: "https://example.org/film", Language: "ru", Attribution: "Synthetic fixture", Text: "Синтетический источник для проверки контракта, не описание фильма.", FetchedAt: testNow}
	source.Spans = []filmresearch.EvidenceSpan{{ID: "plot", Start: 0, End: len(source.Text)}}
	sourceID, err := source.ID()
	must(t, err)
	dossier := filmresearch.Dossier{Version: filmresearch.DossierV1, TMDBID: 603, Title: "Матрица", Year: 1999, SourceIDs: []string{sourceID}}
	dossierID, err := dossier.ID()
	must(t, err)
	p := filmresearch.Preparation{Version: filmresearch.PreparationV1, ChatID: -1, SessionID: sessions[0].ID, WatchID: draft.WatchID, WatchRevision: 1, TMDBID: 603, PolicyVersion: "test-policy", DossierID: dossierID, Generator: filmresearch.Provenance{Provider: "fixture", Model: "fixture", PromptVersion: "v1", ValidatorVersion: "v1"}, Selection: filmresearch.SelectionReceipt{PolicyVersion: "v1", Seed: "test"}}
	for _, pair := range [][4]string{
		{"narrative.investigation", "interest", "interest-5-v1", "investigation_interest_v1"},
		{"world.food_supply", "credibility", "credibility-5-v1", "synthetic_food_credibility_v1"},
		{"style.atmosphere", "enjoyment", "enjoyment-5-v2", "crowded_city_atmosphere_enjoyment_v1"},
		{"world.environmental_crisis", "credibility", "credibility-5-v1", "environmental_crisis_credibility_v1"},
	} {
		options, e := clubprofile.MeasurementOptions(pair[2])
		must(t, e)
		q := clubprofile.Question{Topic: clubprofile.PostWatch, Version: clubprofile.QuestionV2, Intent: pair[1], Scale: pair[2], Text: "Синтетический вопрос о фильме?", Anonymous: true, Revoting: true, Source: "film_research", Options: options, Measurement: &clubprofile.MeasurementDescriptor{TargetID: pair[0], MeasurementID: pair[1], FamilyID: pair[3], TaxonomyVersion: "film-targets-v1", RegistryVersion: "comparison-v1-draft", WordingVersion: "pilot-g-original", TMDBID: 603, Context: "Synthetic, not a factual claim", DossierID: dossierID, Evidence: []clubprofile.EvidenceReference{{SourceID: sourceID, SpanID: "plot"}}}}
		p.Questions = append(p.Questions, q)
		p.Selection.EligibleFamilies = append(p.Selection.EligibleFamilies, pair[3])
		p.Selection.SelectedFamilies = append(p.Selection.SelectedFamilies, pair[3])
	}
	return filmresearch.Bundle{Sources: []filmresearch.SourceSnapshot{source}, Dossier: dossier, Preparation: p}
}

func TestResearchSnapshotsRoundTripIsolationAndNoSideEffects(t *testing.T) {
	s := testStore(t)
	b := researchBundle(t, s)
	before, err := s.ProfileSessions(t.Context(), -1)
	must(t, err)
	id, err := s.SaveResearchPreparation(t.Context(), b, testNow)
	must(t, err)
	retry, err := s.SaveResearchPreparation(t.Context(), b, testNow.Add(time.Hour))
	must(t, err)
	if retry != id {
		t.Fatal("retry changed ID")
	}
	got, err := s.ResearchPreparation(t.Context(), -1, id)
	must(t, err)
	if !reflect.DeepEqual(b, got) {
		t.Fatal("snapshot changed")
	}
	if _, err = s.ResearchPreparation(t.Context(), -2, id); !errors.Is(err, filmresearch.ErrNotFound) {
		t.Fatal("cross-chat read", err)
	}
	f := clubprofile.Film{Metadata: clubprofile.FilmMetadata{Complete: true}, FetchedAt: testNow.Add(time.Hour)}
	must(t, s.SaveFilmEnrichment(t.Context(), 603, &f, testNow))
	got, err = s.ResearchPreparation(t.Context(), -1, id)
	must(t, err)
	if !reflect.DeepEqual(b, got) {
		t.Fatal("mutable cache changed evidence")
	}
	after, err := s.ProfileSessions(t.Context(), -1)
	must(t, err)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("preparation changed session")
	}
	polls, err := s.ProfilePolls(t.Context(), -1, "")
	must(t, err)
	if len(polls) != 0 {
		t.Fatal("preparation published polls")
	}
	var n int
	must(t, s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM film_research_preparations").Scan(&n))
	if n != 1 {
		t.Fatal("duplicate snapshots", n)
	}
}

func TestResearchRejectsStaleForeignAndFrozenSessions(t *testing.T) {
	for _, action := range []string{"foreign-chat", "wrong-film", "revision", "annul", "disabled", "planned", "unknown"} {
		t.Run(action, func(t *testing.T) {
			s := testStore(t)
			b := researchBundle(t, s)
			switch action {
			case "foreign-chat":
				b.Preparation.ChatID = -2
			case "wrong-film":
				_, err := s.db.ExecContext(t.Context(), "INSERT INTO club_films(tmdb_id,title,source,fetched_at) VALUES(999,'Other film','fixture',1)")
				must(t, err)
				_, err = s.db.ExecContext(t.Context(), "UPDATE club_watches SET tmdb_id=999 WHERE id=?", b.Preparation.WatchID)
				must(t, err)
			case "revision":
				b.Preparation.WatchRevision++
			case "annul":
				must(t, s.ChangeProfileWatch(t.Context(), 1002, -1, b.Preparation.WatchID, "annul", "", testNow))
			case "disabled":
				must(t, s.SetAutoReview(t.Context(), 1002, -1, false, testNow))
			case "planned", "unknown":
				q, err := clubprofile.ReviewQuestion(clubprofile.Overall, "Матрица")
				must(t, err)
				id, err := s.PlanProfilePoll(t.Context(), nil, clubprofile.Poll{ChatID: -1, SessionID: b.Preparation.SessionID, Question: q, SlotAt: testNow, Duration: time.Hour})
				must(t, err)
				if action == "unknown" {
					_, err = s.db.ExecContext(t.Context(), "UPDATE club_profile_polls SET state='unknown' WHERE id=?", id)
					must(t, err)
				}
			}
			if _, err := s.SaveResearchPreparation(t.Context(), b, testNow); !errors.Is(err, filmresearch.ErrConflict) {
				t.Fatal(err)
			}
			var n int
			must(t, s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM film_research_sources").Scan(&n))
			if n != 0 {
				t.Fatal("orphan evidence", n)
			}
		})
	}
}

func TestResearchAtomicRollbackAndImmutableRows(t *testing.T) {
	s := testStore(t)
	b := researchBundle(t, s)
	_, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER reject_research BEFORE INSERT ON film_research_preparations BEGIN SELECT RAISE(ABORT,'test'); END`)
	must(t, err)
	if _, err = s.SaveResearchPreparation(t.Context(), b, testNow); err == nil {
		t.Fatal("expected rollback")
	}
	var n int
	must(t, s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM film_research_sources").Scan(&n))
	if n != 0 {
		t.Fatal("partial evidence commit")
	}
	_, err = s.db.ExecContext(t.Context(), "DROP TRIGGER reject_research")
	must(t, err)
	id, err := s.SaveResearchPreparation(t.Context(), b, testNow)
	must(t, err)
	for _, table := range []string{"film_research_sources", "film_research_dossiers", "film_research_preparations"} {
		if _, err = s.db.ExecContext(t.Context(), "UPDATE "+table+" SET snapshot_json='{}'"); err == nil {
			t.Fatal("mutable snapshot", table)
		}
	}
	must(t, s.ChangeProfileWatch(t.Context(), 1003, -1, b.Preparation.WatchID, "annul", "", testNow))
	_, err = s.ResearchPreparation(t.Context(), -1, id)
	must(t, err) // History survives cancellation; it does not authorize publication.
	duplicate, err := s.SaveResearchPreparation(t.Context(), b, testNow)
	must(t, err)
	if duplicate != id {
		t.Fatal("idempotent retry changed snapshot")
	}
	b.Preparation.Questions[0].Text = "Новая версия вопроса после отмены?"
	if _, err = s.SaveResearchPreparation(t.Context(), b, testNow); !errors.Is(err, filmresearch.ErrConflict) {
		t.Fatal(err)
	}
}

func TestResearchUpgradePreservesV1(t *testing.T) {
	s := testStore(t)
	m, err := s.migrator()
	must(t, err)
	must(t, s.EnsureChat(t.Context(), -1))
	addProfileWatch(t, s, -1, 1010, testNow.Format(time.DateOnly), testNow)
	must(t, s.EnsureChat(t.Context(), -2))
	profilePoll(t, s, -2)
	var before string
	must(t, s.db.QueryRowContext(t.Context(), "SELECT question_json FROM club_profile_polls LIMIT 1").Scan(&before))
	_, err = m.DownTo(t.Context(), 21)
	must(t, err)
	_, err = m.Up(t.Context())
	must(t, err)
	var after string
	must(t, s.db.QueryRowContext(t.Context(), "SELECT question_json FROM club_profile_polls LIMIT 1").Scan(&after))
	if before != after {
		t.Fatal("old snapshot rewritten")
	}
	var q clubprofile.Question
	must(t, json.Unmarshal([]byte(after), &q))
	must(t, q.Validate())
	sessions, err := s.ProfileSessions(t.Context(), -1)
	must(t, err)
	if len(sessions) != 1 || !sessions[0].FinishBefore.IsZero() || sessions[0].Plan.Version != clubprofile.CoverageV1 {
		t.Fatal(sessions)
	}
}

func TestResearchRejectsInvalidEvidenceAndSelection(t *testing.T) {
	for name, mutate := range map[string]func(*filmresearch.Bundle){
		"unknown preparation format": func(b *filmresearch.Bundle) { b.Preparation.Version = "future" },
		"unknown dossier format":     func(b *filmresearch.Bundle) { b.Dossier.Version = "future" },
		"incomplete bundle":          func(b *filmresearch.Bundle) { b.Preparation.Questions = b.Preparation.Questions[:3] },
		"duplicate target":           func(b *filmresearch.Bundle) { b.Preparation.Questions[1] = b.Preparation.Questions[0] },
		"changed source":             func(b *filmresearch.Bundle) { b.Sources[0].Text += " Changed." },
		"wrong film descriptor":      func(b *filmresearch.Bundle) { b.Preparation.Questions[0].Measurement.TMDBID = 999 },
		"foreign source":             func(b *filmresearch.Bundle) { b.Preparation.Questions[0].Measurement.Evidence[0].SourceID = "foreign" },
		"invented span":              func(b *filmresearch.Bundle) { b.Preparation.Questions[0].Measurement.Evidence[0].SpanID = "invented" },
		"foreign dossier":            func(b *filmresearch.Bundle) { b.Preparation.Questions[0].Measurement.DossierID = "foreign" },
		"not eligible":               func(b *filmresearch.Bundle) { b.Preparation.Selection.EligibleFamilies = nil },
		"not selected":               func(b *filmresearch.Bundle) { b.Preparation.Selection.SelectedFamilies[0] = "other" },
		"selected and rejected": func(b *filmresearch.Bundle) {
			b.Preparation.Selection.Rejected = []filmresearch.RejectedFamily{{FamilyID: b.Preparation.Selection.SelectedFamilies[0], Reason: "inapplicable"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := testStore(t)
			b := researchBundle(t, s)
			mutate(&b)
			if _, err := s.SaveResearchPreparation(t.Context(), b, testNow); err == nil {
				t.Fatal("invalid bundle saved")
			}
		})
	}
}

func TestResearchDatabaseScopeAndDeliveryGate(t *testing.T) {
	s := testStore(t)
	b := researchBundle(t, s)
	id, err := s.SaveResearchPreparation(t.Context(), b, testNow)
	must(t, err)
	must(t, s.EnsureChat(t.Context(), -2))
	_, err = s.db.ExecContext(t.Context(), `INSERT INTO film_research_preparations
 SELECT printf('%064d',1),-2,session_id,watch_id,watch_revision,tmdb_id,dossier_id,policy_version,snapshot_json,created_at
 FROM film_research_preparations WHERE id=?`, id)
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatal("composite FK allowed foreign chat")
	}
	foreign := b.Sources[0]
	foreign.TMDBID = 999
	foreignID, err := foreign.ID()
	must(t, err)
	raw, err := json.Marshal(foreign)
	must(t, err)
	_, err = s.db.ExecContext(t.Context(), `INSERT INTO film_research_sources(id,tmdb_id,snapshot_json,created_at) VALUES(?,999,?,1)`, foreignID, raw)
	must(t, err)
	_, err = s.db.ExecContext(t.Context(), `INSERT INTO film_research_dossier_sources(dossier_id,source_id,tmdb_id) VALUES(?,?,999)`, b.Preparation.DossierID, foreignID)
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatal("film identity FK missing")
	}
	_, err = s.PlanProfilePoll(t.Context(), nil, clubprofile.Poll{ChatID: -1, SessionID: b.Preparation.SessionID, Question: b.Preparation.Questions[0], SlotAt: testNow, Duration: time.Hour})
	if !errors.Is(err, clubprofile.ErrUnapprovedMeasurement) {
		t.Fatal("draft entered scheduler", err)
	}
}
