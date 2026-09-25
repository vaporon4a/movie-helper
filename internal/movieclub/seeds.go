package movieclub

const (
	seedScienceFiction = "science_fiction"
	seedDrama          = "drama"
	seedComedy         = "comedy"
	seedAdventure      = "adventure"
	seedRussian        = "russian"
)

type ReferenceSeed struct {
	ID     int64
	Title  string
	Group  string
	Decade int
}

// ReferenceSeeds is deliberately curated instead of being driven by current
// popularity, so each poll can mix recognizable films, genres and decades.
var ReferenceSeeds = []ReferenceSeed{
	{603, "Матрица", seedScienceFiction, 1990},
	{27205, "Начало", seedScienceFiction, 2010},
	{157336, "Интерстеллар", seedScienceFiction, 2010},
	{78, "Бегущий по лезвию", seedScienceFiction, 1980},
	{18, "Пятый элемент", seedScienceFiction, 1990},
	{120, "Властелин колец: Братство Кольца", "fantasy", 2000},
	{129, "Унесённые призраками", "fantasy", 2000},
	{1417, "Лабиринт Фавна", "fantasy", 2000},
	{680, "Криминальное чтиво", "crime", 1990},
	{238, "Крёстный отец", "crime", 1970},
	{101, "Леон", "crime", 1990},
	{807, "Семь", "crime", 1990},
	{550, "Бойцовский клуб", seedDrama, 1990},
	{13, "Форрест Гамп", seedDrama, 1990},
	{496243, "Паразиты", seedDrama, 2010},
	{244786, "Одержимость", seedDrama, 2010},
	{389, "12 разгневанных мужчин", seedDrama, 1950},
	{194, "Амели", "romance", 2000},
	{38, "Вечное сияние чистого разума", "romance", 2000},
	{597, "Титаник", "romance", 1990},
	{105, "Назад в будущее", seedComedy, 1980},
	{137, "День сурка", seedComedy, 1990},
	{37165, "Шоу Трумана", seedComedy, 1990},
	{120467, "Отель «Гранд Будапешт»", seedComedy, 2010},
	{77338, "1+1", seedComedy, 2010},
	{694, "Сияние", "horror", 1980},
	{348, "Чужой", "horror", 1970},
	{539, "Психо", "horror", 1960},
	{329, "Парк Юрского периода", seedAdventure, 1990},
	{98, "Гладиатор", seedAdventure, 2000},
	{76341, "Безумный Макс: Дорога ярости", seedAdventure, 2010},
	{11, "Звёздные войны", seedAdventure, 1970},
	{862, "История игрушек", "animation", 1990},
	{10681, "ВАЛЛ-И", "animation", 2000},
	{372058, "Твоё имя", "animation", 2010},
	{155, "Тёмный рыцарь", "superhero", 2000},
	{299536, "Мстители: Война бесконечности", "superhero", 2010},
	{497, "Зелёная миля", "book_adaptation", 1990},
	{671, "Гарри Поттер и философский камень", "book_adaptation", 2000},
	{11036, "Дневник памяти", "book_adaptation", 2000},
	{1398, "Сталкер", seedRussian, 1970},
	{593, "Солярис", seedRussian, 1970},
	{20803, "Иван Васильевич меняет профессию", seedRussian, 1970},
	{21028, "Москва слезам не верит", seedRussian, 1980},
	{20992, "Брат", seedRussian, 1990},
	{218, "Терминатор", "action", 1980},
	{562, "Крепкий орешек", "action", 1980},
	{19995, "Аватар", seedAdventure, 2000},
}
