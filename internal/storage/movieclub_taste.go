package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/vaporon4a/movie-helper/internal/movieclub"
)

const maxMovieJSONBytes = 4096

func (s *Store) MoviePreferenceSettings(ctx context.Context, chatID int64, now time.Time) (movieclub.PreferenceSettings, error) {
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO movie_preference_settings(chat_id,mode,effective_from,policy_version,updated_at)
 VALUES(?,'shadow',0,?,?)`, chatID, movieclub.RankingPolicyV1, now.Unix()); err != nil {
		return movieclub.PreferenceSettings{}, err
	}
	var value movieclub.PreferenceSettings
	err := s.db.QueryRowContext(ctx, `SELECT chat_id,mode,effective_from,policy_version,updated_at
 FROM movie_preference_settings WHERE chat_id=?`, chatID).Scan(&value.ChatID, &value.Mode, &value.EffectiveFrom, &value.PolicyVersion, &value.UpdatedAt)
	if err == nil && !value.Mode.Valid() {
		return value, errors.New("invalid movie personalization mode")
	}
	return value, err
}

func (s *Store) SetMoviePreferenceMode(ctx context.Context, operationID, chatID int64, mode movieclub.PersonalizationMode, now time.Time) error {
	if !mode.Valid() {
		return errors.New("invalid movie personalization mode")
	}
	return s.transaction(ctx, &operationID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO movie_preference_settings(chat_id,mode,effective_from,policy_version,updated_at)
 VALUES(?,?,0,?,?) ON CONFLICT(chat_id) DO UPDATE SET mode=excluded.mode,policy_version=excluded.policy_version,updated_at=excluded.updated_at`,
			chatID, mode, movieclub.RankingPolicyV1, now.Unix())
		return changedAtLeastOne(result, err)
	})
}

func (s *Store) ResetMovieTaste(ctx context.Context, operationID, chatID int64, now time.Time) error {
	return s.transaction(ctx, &operationID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO movie_preference_settings(chat_id,mode,effective_from,policy_version,updated_at)
 VALUES(?,'shadow',?,?,?) ON CONFLICT(chat_id) DO UPDATE SET effective_from=excluded.effective_from,updated_at=excluded.updated_at`,
			chatID, now.Unix(), movieclub.RankingPolicyV1, now.Unix())
		return changedAtLeastOne(result, err)
	})
}

func changedAtLeastOne(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows < 1 {
		return movieclub.ErrConflict
	}
	return nil
}

func (s *Store) MovieTasteHistory(ctx context.Context, chatID, excludeRoundID int64, before time.Time) ([]movieclub.TasteRound, error) {
	settings, err := s.MoviePreferenceSettings(ctx, chatID, before)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.feature,CASE WHEN r.closes_at>0 THEN r.closes_at ELSE r.slot_at END,
 o.round_id,o.position,o.option_kind,o.provider_id,o.label,o.votes,o.metadata_json,o.selection_role,o.selection_score,o.policy_version
 FROM movie_rounds r JOIN movie_poll_options o ON o.round_id=r.id
 WHERE r.chat_id=? AND r.id<>? AND r.slot_at<? AND r.slot_at>=?
 AND r.state IN ('selecting','ready','publishing','published')
 ORDER BY r.id,o.position`, chatID, excludeRoundID, before.Unix(), settings.EffectiveFrom)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []movieclub.TasteRound
	var current *movieclub.TasteRound
	for rows.Next() {
		var roundID, closedAt int64
		var feature movieclub.Feature
		var option movieclub.Option
		var metadata string
		if err = rows.Scan(&roundID, &feature, &closedAt, &option.RoundID, &option.Position, &option.Kind, &option.ProviderID,
			&option.Label, &option.Votes, &metadata, &option.SelectionRole, &option.SelectionScore, &option.PolicyVersion); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].ID != roundID {
			out = append(out, movieclub.TasteRound{ID: roundID, ClosedAt: closedAt, Feature: feature})
		}
		current = &out[len(out)-1]
		if option.Metadata, err = decodeMovieMetadata(metadata); err != nil {
			option.Metadata = movieclub.MovieMetadata{}
		}
		current.Options = append(current.Options, option)
	}
	return out, rows.Err()
}

func (s *Store) MovieExposureCounts(ctx context.Context, chatID int64, since time.Time) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.tmdb_id,count(*) FROM movie_recommendations m
 JOIN movie_rounds r ON r.id=m.round_id
 WHERE r.chat_id=? AND r.state='published' AND r.slot_at>=? GROUP BY m.tmdb_id`, chatID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]int)
	for rows.Next() {
		var id int64
		var count int
		if err = rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		out[id] = count
	}
	return out, rows.Err()
}

func (s *Store) SaveMovieOptionMetadata(ctx context.Context, roundID int64, position int, metadata movieclub.MovieMetadata) error {
	payload, err := encodeMovieMetadata(metadata)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE movie_poll_options SET metadata_json=?
 WHERE round_id=? AND position=? AND metadata_json='{}'`, payload, roundID, position)
	if err != nil {
		return err
	}
	_, err = result.RowsAffected()
	return err
}

func insertRankingCandidate(ctx context.Context, tx *sql.Tx, roundID int64, candidate movieclub.RankingCandidate) error {
	if candidate.TMDBID <= 0 || len(candidate.SourceBucket) == 0 || len(candidate.SourceBucket) > 64 ||
		(candidate.SelectedMode != movieclub.LegacyPolicyVersion && candidate.SelectedMode != "adaptive" && candidate.SelectedMode != "none") ||
		!finite(candidate.RankingScore) {
		return errors.New("invalid movie ranking candidate")
	}
	metadata, err := encodeMovieMetadata(movieclub.MovieMetadata{GenreIDs: candidate.Movie.Genres, ReleaseYear: candidate.Movie.Year})
	if err != nil {
		return err
	}
	breakdown, err := encodeRankingBreakdown(candidate.Ranking)
	if err != nil {
		return err
	}
	var legacy, adaptive any
	if candidate.LegacyPosition >= 0 {
		legacy = candidate.LegacyPosition
	}
	if candidate.AdaptivePosition >= 0 {
		adaptive = candidate.AdaptivePosition
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO movie_ranking_candidates(round_id,source_bucket,tmdb_id,metadata_json,legacy_position,adaptive_position,
 ranking_score,ranking_breakdown_json,policy_version,selected_mode,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,strftime('%s','now'))`,
		roundID, candidate.SourceBucket, candidate.TMDBID, metadata, legacy, adaptive, candidate.RankingScore, breakdown,
		candidate.PolicyVersion, candidate.SelectedMode)
	return err
}

func optionRole(option movieclub.Option) movieclub.SelectionRole {
	if option.SelectionRole == "" {
		return movieclub.SelectionLegacy
	}
	return option.SelectionRole
}

func optionPolicy(option movieclub.Option) string {
	if strings.TrimSpace(option.PolicyVersion) == "" {
		return movieclub.LegacyPolicyVersion
	}
	return option.PolicyVersion
}

func recommendationPolicy(value movieclub.Recommendation) string {
	if strings.TrimSpace(value.PolicyVersion) == "" {
		return movieclub.LegacyPolicyVersion
	}
	return value.PolicyVersion
}

func encodeMovieMetadata(value movieclub.MovieMetadata) (string, error) {
	value.GenreIDs = validGenreIDs(value.GenreIDs)
	if value.ReleaseYear != 0 && (value.ReleaseYear < 1870 || value.ReleaseYear > 2100) {
		return "", errors.New("invalid movie release year")
	}
	return encodeBoundedJSON(value)
}

func decodeMovieMetadata(raw string) (movieclub.MovieMetadata, error) {
	var value movieclub.MovieMetadata
	if err := decodeBoundedJSON(raw, &value); err != nil {
		return value, err
	}
	encoded, err := encodeMovieMetadata(value)
	if err != nil || len(encoded) == 0 {
		return value, err
	}
	value.GenreIDs = validGenreIDs(value.GenreIDs)
	return value, nil
}

func encodeGenreIDs(ids []int64) (string, error) { return encodeBoundedJSON(validGenreIDs(ids)) }

func decodeGenreIDs(raw string) ([]int64, error) {
	var ids []int64
	if err := decodeBoundedJSON(raw, &ids); err != nil {
		return nil, err
	}
	return validGenreIDs(ids), nil
}

func encodeRankingBreakdown(value movieclub.RankingBreakdown) (string, error) {
	if !finite(value.Quality) || !finite(value.Affinity) || !finite(value.Novelty) || !finite(value.Exploration) || !finite(value.SourceRelevance) {
		return "", errors.New("invalid movie ranking breakdown")
	}
	return encodeBoundedJSON(value)
}

func decodeRankingBreakdown(raw string) (movieclub.RankingBreakdown, error) {
	var value movieclub.RankingBreakdown
	if err := decodeBoundedJSON(raw, &value); err != nil {
		return value, err
	}
	_, err := encodeRankingBreakdown(value)
	return value, err
}

func encodeBoundedJSON(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(payload) > maxMovieJSONBytes {
		return "", errors.New("movie metadata is too large")
	}
	return string(payload), nil
}

func decodeBoundedJSON(raw string, target any) error {
	if len(raw) == 0 || len(raw) > maxMovieJSONBytes {
		return errors.New("invalid movie metadata size")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid trailing movie metadata: %w", err)
	}
	return nil
}

func validGenreIDs(ids []int64) []int64 {
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
