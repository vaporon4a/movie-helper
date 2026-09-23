package movieclub

import (
	"math/rand/v2"
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

func GenreOptions(seed uint64) []Option {
	copyOf := append([]Option(nil), genres...)
	// #nosec G404 -- deterministic product rotation is intentional, not security-sensitive.
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	r.Shuffle(len(copyOf), func(i, j int) { copyOf[i], copyOf[j] = copyOf[j], copyOf[i] })
	copyOf = copyOf[:10]
	for i := range copyOf {
		copyOf[i].Position = i
	}
	return copyOf
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
