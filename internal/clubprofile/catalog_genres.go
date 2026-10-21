package clubprofile

import "math/rand/v2"

var genres = []Option{
	{ProviderID: 28, Text: "Боевик"},
	{ProviderID: 12, Text: "Приключения"},
	{ProviderID: 16, Text: "Анимация"},
	{ProviderID: 35, Text: "Комедия"},
	{ProviderID: 80, Text: "Криминал"},
	{ProviderID: 99, Text: "Документальный"},
	{ProviderID: 18, Text: "Драма"},
	{ProviderID: 10751, Text: "Семейный"},
	{ProviderID: 14, Text: "Фэнтези"},
	{ProviderID: 36, Text: "Исторический"},
	{ProviderID: 27, Text: "Ужасы"},
	{ProviderID: 10402, Text: "Музыка"},
	{ProviderID: 9648, Text: "Детектив"},
	{ProviderID: 10749, Text: "Мелодрама"},
	{ProviderID: 878, Text: "Фантастика"},
	{ProviderID: 53, Text: "Триллер"},
	{ProviderID: 10752, Text: "Военный"},
	{ProviderID: 37, Text: "Вестерн"},
}

func genreOptions(seed uint64) []Option {
	options := append([]Option(nil), genres...)
	// #nosec G404 -- deterministic rotation, not a security-sensitive random choice.
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	r.Shuffle(len(options), func(i, j int) { options[i], options[j] = options[j], options[i] })
	return options[:10]
}
