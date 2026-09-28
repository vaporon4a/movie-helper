package movieclub

import (
	"context"
	"log/slog"
	"time"
)

func loadTaste(ctx context.Context, source any, catalog ReferenceCatalog, chatID, excludeRoundID int64, now time.Time) (PreferenceSettings, ChatTasteProfile, map[int64]int, error) {
	settings := PreferenceSettings{ChatID: chatID, Mode: PersonalizationOff, PolicyVersion: RankingPolicyV1}
	history, ok := source.(TasteHistory)
	if !ok {
		return settings, ProjectTaste(nil, now), nil, nil
	}
	var err error
	settings, err = history.MoviePreferenceSettings(ctx, chatID, now)
	if err != nil {
		return settings, ChatTasteProfile{}, nil, err
	}
	rounds, err := history.MovieTasteHistory(ctx, chatID, excludeRoundID, now)
	if err != nil {
		return settings, ChatTasteProfile{}, nil, err
	}
	backfillTasteMetadata(ctx, source, catalog, rounds)
	exposures, err := history.MovieExposureCounts(ctx, chatID, now.Add(-365*24*time.Hour))
	if err != nil {
		return settings, ChatTasteProfile{}, nil, err
	}
	profile := ProjectTaste(rounds, now)
	slog.DebugContext(ctx, "movieclub taste projected", "chat_id", chatID, "policy_version", settings.PolicyVersion,
		"mode", settings.Mode, "completed_rounds", profile.CompletedRounds, "effective_votes", profile.EffectiveVotes,
		"skipped_records", profile.SkippedRecords)
	return settings, profile, exposures, nil
}

func backfillTasteMetadata(ctx context.Context, source any, catalog ReferenceCatalog, rounds []TasteRound) {
	if catalog == nil {
		return
	}
	cache, _ := source.(TasteMetadataStore)
	remaining := 2
	for roundIndex := range rounds {
		remaining = backfillTasteRound(ctx, cache, catalog, &rounds[roundIndex], remaining)
		if remaining == 0 {
			return
		}
	}
}

func backfillTasteRound(ctx context.Context, cache TasteMetadataStore, catalog ReferenceCatalog, round *TasteRound, remaining int) int {
	for index := range round.Options {
		option := &round.Options[index]
		if option.Kind != OptionMovie || len(option.Metadata.GenreIDs) > 0 {
			continue
		}
		backfillTasteOption(ctx, cache, catalog, option)
		remaining--
		if remaining == 0 {
			return 0
		}
	}
	return remaining
}

func backfillTasteOption(ctx context.Context, cache TasteMetadataStore, catalog ReferenceCatalog, option *Option) {
	details, err := catalog.Details(ctx, option.ProviderID)
	if err != nil {
		return
	}
	option.Metadata = MovieMetadata{GenreIDs: details.Genres, ReleaseYear: details.Year}
	if cache != nil {
		if err = cache.SaveMovieOptionMetadata(ctx, option.RoundID, option.Position, option.Metadata); err != nil {
			slog.WarnContext(ctx, "movieclub taste metadata cache failed", "round_id", option.RoundID,
				"position", option.Position, "reason", "storage_error")
		}
	}
}

func overlapAt(first, second []Movie, limit int) int {
	seen := make(map[int64]bool, min(limit, len(first)))
	for _, movie := range first[:min(limit, len(first))] {
		seen[movie.ID] = true
	}
	overlap := 0
	for _, movie := range second[:min(limit, len(second))] {
		if seen[movie.ID] {
			overlap++
		}
	}
	return overlap
}

func rankingCandidates(roundID int64, bucket string, legacy []Movie, adaptive []RankedMovie) []RankingCandidate {
	legacyPositions := make(map[int64]int, len(legacy))
	adaptivePositions := make(map[int64]int, len(adaptive))
	byID := make(map[int64]RankedMovie, len(adaptive))
	for index, movie := range legacy {
		legacyPositions[movie.ID] = index
	}
	for index, movie := range adaptive {
		adaptivePositions[movie.ID] = index
		byID[movie.ID] = movie
	}
	ids := make([]int64, 0, len(legacy)+len(adaptive))
	seen := make(map[int64]bool, cap(ids))
	for _, movie := range legacy {
		if !seen[movie.ID] {
			seen[movie.ID] = true
			ids = append(ids, movie.ID)
		}
	}
	for _, movie := range adaptive {
		if !seen[movie.ID] {
			seen[movie.ID] = true
			ids = append(ids, movie.ID)
		}
	}
	out := make([]RankingCandidate, 0, len(ids))
	for _, id := range ids {
		legacyPosition, inLegacy := legacyPositions[id]
		adaptivePosition, inAdaptive := adaptivePositions[id]
		if !inLegacy {
			legacyPosition = -1
		}
		if !inAdaptive {
			adaptivePosition = -1
		}
		ranked := byID[id]
		if ranked.ID == 0 {
			for _, movie := range legacy {
				if movie.ID == id {
					ranked.Movie = movie
					break
				}
			}
		}
		out = append(out, RankingCandidate{
			RoundID: roundID, TMDBID: id, SourceBucket: bucket, SelectedMode: "none",
			Movie: ranked.Movie, LegacyPosition: legacyPosition, AdaptivePosition: adaptivePosition,
			RankingScore: ranked.Score, Ranking: ranked.Breakdown, PolicyVersion: RankingPolicyV1,
		})
	}
	return out
}

func markCandidateSelections(candidates []RankingCandidate, legacy, adaptive []Recommendation, mode PersonalizationMode) {
	selected := legacy
	label := LegacyPolicyVersion
	if mode == PersonalizationOn {
		selected = adaptive
		label = "adaptive"
	}
	for index := range candidates {
		for _, movie := range selected {
			if candidates[index].TMDBID == movie.ID && candidateBucketMatches(candidates[index].SourceBucket, movie.Relation) {
				candidates[index].SelectedMode = label
				break
			}
		}
	}
}

func candidateBucketMatches(bucket, relation string) bool {
	if len(bucket) >= 6 && bucket[:6] == "genre:" {
		return relation == "top"
	}
	return bucket == relation
}
