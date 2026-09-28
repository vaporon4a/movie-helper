package movieclub

import (
	"math"
	"slices"
	"time"
)

const (
	tasteAlpha        = 5.0
	tasteTurnoutPrior = 5.0
	tasteHalfLifeDays = 120.0
)

// ProjectTaste builds a chat-level profile from completed anonymous polls.
// It intentionally treats missing votes as unknown instead of negative feedback.
func ProjectTaste(rounds []TasteRound, now time.Time) ChatTasteProfile {
	profile := ChatTasteProfile{
		GenreAffinity:  make(map[int64]float64),
		DecadeAffinity: make(map[int]float64),
		GenreExposure:  make(map[int64]int),
		DecadeExposure: make(map[int]int),
	}
	for _, round := range rounds {
		projectTasteRound(&profile, round, now)
	}
	normalizeTaste(profile.GenreAffinity)
	normalizeTaste(profile.DecadeAffinity)
	return profile
}

func projectTasteRound(profile *ChatTasteProfile, round TasteRound, now time.Time) {
	if len(round.Options) < 2 {
		profile.SkippedRecords++
		return
	}
	totalVotes := 0
	for _, option := range round.Options {
		totalVotes += max(0, option.Votes)
		addTasteExposure(profile, option)
	}
	if totalVotes == 0 {
		return
	}
	ageDays := max(0.0, now.Sub(time.Unix(round.ClosedAt, 0)).Hours()/24)
	recency := math.Pow(0.5, ageDays/tasteHalfLifeDays)
	confidence := float64(totalVotes) / (float64(totalVotes) + tasteTurnoutPrior)
	baseline := 1.0 / float64(len(round.Options))
	for _, option := range round.Options {
		share := (float64(option.Votes) + tasteAlpha/float64(len(round.Options))) / (float64(totalVotes) + tasteAlpha)
		addTasteSignal(profile, option, math.Max(0, share-baseline)*confidence*recency)
	}
	profile.EffectiveVotes += float64(totalVotes) * recency
	profile.CompletedRounds++
}

func addTasteExposure(profile *ChatTasteProfile, option Option) {
	for _, genreID := range optionGenreIDs(option) {
		profile.GenreExposure[genreID]++
	}
	if decade := releaseDecade(option.Metadata.ReleaseYear); decade != 0 {
		profile.DecadeExposure[decade]++
	}
}

func addTasteSignal(profile *ChatTasteProfile, option Option, signal float64) {
	genreIDs := optionGenreIDs(option)
	if len(genreIDs) > 0 {
		perGenre := signal / float64(len(genreIDs))
		for _, genreID := range genreIDs {
			profile.GenreAffinity[genreID] += perGenre
		}
	}
	if decade := releaseDecade(option.Metadata.ReleaseYear); decade != 0 {
		profile.DecadeAffinity[decade] += signal * 0.35
	}
}

func optionGenreIDs(option Option) []int64 {
	if option.Kind == OptionGenre && option.ProviderID > 0 {
		return []int64{option.ProviderID}
	}
	return validGenreIDs(option.Metadata.GenreIDs)
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
	slices.Sort(out)
	return out
}

func releaseDecade(year int) int {
	if year < 1870 || year > 2100 {
		return 0
	}
	return year / 10 * 10
}

func normalizeTaste[K comparable](values map[K]float64) {
	maximum := 0.0
	for _, value := range values {
		maximum = math.Max(maximum, value)
	}
	if maximum == 0 {
		return
	}
	for key := range values {
		values[key] /= maximum
	}
}

func profileWeight(effectiveVotes float64) float64 {
	return math.Min(0.8, effectiveVotes/(effectiveVotes+20))
}
