package movieclub

import (
	"math/rand/v2"
	"slices"
)

var genres = []Option{
	{ProviderID: 28, Kind: OptionGenre, Label: "Боевик"},
	{ProviderID: 12, Kind: OptionGenre, Label: "Приключения"},
	{ProviderID: 16, Kind: OptionGenre, Label: "Анимация"},
	{ProviderID: 35, Kind: OptionGenre, Label: "Комедия"},
	{ProviderID: 80, Kind: OptionGenre, Label: "Криминал"},
	{ProviderID: 99, Kind: OptionGenre, Label: "Документальный"},
	{ProviderID: 18, Kind: OptionGenre, Label: "Драма"},
	{ProviderID: 10751, Kind: OptionGenre, Label: "Семейный"},
	{ProviderID: 14, Kind: OptionGenre, Label: "Фэнтези"},
	{ProviderID: 36, Kind: OptionGenre, Label: "Исторический"},
	{ProviderID: 27, Kind: OptionGenre, Label: "Ужасы"},
	{ProviderID: 10402, Kind: OptionGenre, Label: "Музыка"},
	{ProviderID: 9648, Kind: OptionGenre, Label: "Детектив"},
	{ProviderID: 10749, Kind: OptionGenre, Label: "Мелодрама"},
	{ProviderID: 878, Kind: OptionGenre, Label: "Фантастика"},
	{ProviderID: 53, Kind: OptionGenre, Label: "Триллер"},
	{ProviderID: 10752, Kind: OptionGenre, Label: "Военный"},
	{ProviderID: 37, Kind: OptionGenre, Label: "Вестерн"},
}

func GenreLabel(id int64) string {
	for _, genre := range genres {
		if genre.ProviderID == id {
			return genre.Label
		}
	}
	return ""
}

func GenreOptions(seed uint64) []Option {
	copyOf := append([]Option(nil), genres...)
	// #nosec G404 -- deterministic product rotation is intentional, not security-sensitive.
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	r.Shuffle(len(copyOf), func(i, j int) { copyOf[i], copyOf[j] = copyOf[j], copyOf[i] })
	copyOf = copyOf[:10]
	for i := range copyOf {
		copyOf[i].Position = i
		copyOf[i].Metadata = MovieMetadata{GenreIDs: []int64{copyOf[i].ProviderID}}
		copyOf[i].SelectionRole = SelectionLegacy
		copyOf[i].PolicyVersion = LegacyPolicyVersion
	}
	return copyOf
}

func AdaptiveGenreOptions(seed uint64, profile ChatTasteProfile) []Option {
	candidates := append([]Option(nil), genres...)
	// #nosec G404 -- stable shuffle supplies deterministic tie-breaking.
	random := rand.New(rand.NewPCG(seed, seed^0xa0761d6478bd642f))
	random.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	exploit := append([]Option(nil), candidates...)
	slices.SortStableFunc(exploit, func(a, b Option) int {
		if profile.GenreAffinity[a.ProviderID] > profile.GenreAffinity[b.ProviderID] {
			return -1
		}
		if profile.GenreAffinity[a.ProviderID] < profile.GenreAffinity[b.ProviderID] {
			return 1
		}
		return 0
	})
	explore := append([]Option(nil), candidates...)
	slices.SortStableFunc(explore, func(a, b Option) int {
		return profile.GenreExposure[a.ProviderID] - profile.GenreExposure[b.ProviderID]
	})
	builder := genreOptionBuilder{profile: profile, used: make(map[int64]bool, 10), options: make([]Option, 0, 10)}
	builder.addPositive(exploit, 6)
	builder.addCount(explore, SelectionExplore, 3)
	builder.addCount(candidates, SelectionWildcard, 1)
	builder.addCount(exploit, SelectionExploit, 10)
	return builder.finish()
}

type genreOptionBuilder struct {
	profile ChatTasteProfile
	used    map[int64]bool
	options []Option
}

func (b *genreOptionBuilder) add(option Option, role SelectionRole) bool {
	if len(b.options) == 10 || b.used[option.ProviderID] {
		return false
	}
	b.used[option.ProviderID] = true
	option.SelectionRole = role
	option.SelectionScore = b.profile.GenreAffinity[option.ProviderID]
	option.PolicyVersion = RankingPolicyV1
	option.Metadata = MovieMetadata{GenreIDs: []int64{option.ProviderID}}
	b.options = append(b.options, option)
	return true
}

func (b *genreOptionBuilder) addPositive(options []Option, limit int) {
	for _, option := range options {
		if len(b.options) == limit || b.profile.GenreAffinity[option.ProviderID] <= 0 {
			return
		}
		b.add(option, SelectionExploit)
	}
}

func (b *genreOptionBuilder) addCount(options []Option, role SelectionRole, count int) {
	added := 0
	for _, option := range options {
		if added == count || len(b.options) == 10 {
			return
		}
		if b.add(option, role) {
			added++
		}
	}
}

func (b *genreOptionBuilder) finish() []Option {
	for index := range b.options {
		b.options[index].Position = index
	}
	return b.options
}

func Winners(options []Option, seed uint64) []Option {
	maxVotes := 0
	for _, option := range options {
		maxVotes = max(maxVotes, option.Votes)
	}
	if maxVotes == 0 {
		return nil
	}
	var tied []Option
	for _, option := range options {
		if option.Votes == maxVotes {
			tied = append(tied, option)
		}
	}
	if len(tied) <= 2 {
		return tied
	}
	// #nosec G404 -- deterministic tie-breaking must survive restarts.
	r := rand.New(rand.NewPCG(seed, seed^0xd1b54a32d192ed03))
	r.Shuffle(len(tied), func(i, j int) { tied[i], tied[j] = tied[j], tied[i] })
	return tied[:2]
}
