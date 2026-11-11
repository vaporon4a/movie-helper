package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (h *Handler) showStatistics(ctx context.Context, chat int64, message int, page string, id int64) error {
	if err := h.prepareProfilePanel(ctx, chat, message, page, id); err != nil {
		return err
	}
	text, keys, err := h.statisticsPage(ctx, chat, page, id)
	if err != nil {
		return err
	}
	_, err = h.deliverPanel(ctx, chat, message, "profile", text, closableKeyboard(keys))
	if err != nil {
		return err
	}
	return errResponseSent
}
func (h *Handler) statisticsPage(ctx context.Context, chat int64, page string, id int64) (string, *models.InlineKeyboardMarkup, error) {
	if page == "stats_archive" || page == "stats_report" || page == "stats_proof" {
		return h.statisticsArchivePage(ctx, chat, page, id)
	}
	if page == "stats_settings" {
		return h.statisticsSettingsPage(ctx, chat)
	}
	keys := statisticsNavigation()
	if page == "stats_rules" {
		return statisticsRules, keys, nil
	}
	if page == "stats_preview" {
		s, err := h.Statistics.Preview(ctx, chat)
		if err != nil {
			return "", keys, err
		}
		return "👀 Предпросмотр ближайшей сводки\nПо данным на сейчас; до выпуска результаты могут дополниться.\n\n" + clubprofile.RenderStatisticsDigest(s), keys, nil
	}
	mode := strings.TrimPrefix(page, "stats_")
	s, err := h.Statistics.Snapshot(ctx, chat, mode)
	if err != nil {
		return "", keys, err
	}
	if page == "stats_films" {
		return statisticsFilmPage(s, keys, int(id))
	}
	var b strings.Builder
	b.WriteString("📊 Что выяснил киноклуб\n")
	appendStatisticsPeriod(&b, s)
	fmt.Fprintf(&b, "\nФильмов в истории: %d\nЗавершённых опросов за период: %d · ответов: %d\nО просмотрах: %d · жанры и фильмы: %d\n", s.Films, s.Polls, s.Answers, s.PostWatch, s.Ordinary)
	for _, c := range clubprofile.SelectStatisticsClaims(s.Claims, 3) {
		b.WriteString("\n" + c.Text + "\n")
		keys.InlineKeyboard = append(keys.InlineKeyboard, []models.InlineKeyboardButton{profileButton(fmt.Sprintf("🔎 Ответы · опрос #%d", c.PollID), "poll", c.PollID)})
	}
	if len(s.Claims) == 0 {
		b.WriteString("\nСодержательных итогов пока мало. Открытые опросы дождутся завершения.\n")
	}
	fmt.Fprintf(&b, "\nОтветы — не уникальные участники. Исторические опросы отдельно: %d.\nПовторные вопросы не складываем. Общие выводы требуют сопоставимых ответов по нескольким фильмам.", s.Legacy)
	return b.String(), keys, nil
}
func statisticsNavigation() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{profileButton("Все итоги", "stats", 0), profileButton("За 7 дней", "stats_week", 0), profileButton("За месяц", "stats_month", 0)},
		{profileButton("🎞 По фильмам", "stats_films", 0), profileButton("ℹ️ Как считаем", "stats_rules", 0)},
		{profileButton("🗓 Недельная сводка", "stats_settings", 0), profileButton("🗂 Выпуски", "stats_archive", 0)},
		{profileButton("← Профиль", "overview", 0)},
	}}
}
func appendStatisticsPeriod(b *strings.Builder, s clubprofile.StatisticsSnapshot) {
	loc, err := time.LoadLocation(s.Zone)
	if err != nil {
		loc = time.UTC
	}
	if s.Period.Start.IsZero() {
		fmt.Fprintf(b, "Все накопленные итоги · на %s\n", s.AsOf.In(loc).Format("02.01.2006 15:04"))
		return
	}
	fmt.Fprintf(b, "%s — %s · %s\nТекущий период ещё продолжается.\n", s.Period.Start.Format("02.01.2006 15:04"), s.Period.End.Format("02.01.2006 15:04"), s.Zone)
}
func statisticsFilmPage(s clubprofile.StatisticsSnapshot, keys *models.InlineKeyboardMarkup, offset int) (string, *models.InlineKeyboardMarkup, error) {
	var claims []clubprofile.StatisticsClaim
	for _, c := range s.Claims {
		if c.FilmID != 0 {
			claims = append(claims, c)
		}
	}
	if offset < 0 || offset >= len(claims) {
		offset = 0
	}
	end := min(offset+3, len(claims))
	var b strings.Builder
	b.WriteString("🎞 Впечатления о просмотренном\n\n")
	for _, c := range claims[offset:end] {
		b.WriteString(c.Text + "\n\n")
		keys.InlineKeyboard = append(keys.InlineKeyboard, []models.InlineKeyboardButton{profileButton(fmt.Sprintf("Ответы #%d", c.PollID), "poll", c.PollID)})
	}
	if len(claims) == 0 {
		b.WriteString("Пока нет достаточно полных оценок. Все сохранённые ответы доступны в карточках просмотров.")
	}
	if offset > 0 {
		keys.InlineKeyboard = append(keys.InlineKeyboard, []models.InlineKeyboardButton{profileButton("← Предыдущие", "stats_films", int64(max(0, offset-3)))})
	}
	if end < len(claims) {
		keys.InlineKeyboard = append(keys.InlineKeyboard, []models.InlineKeyboardButton{profileButton("Следующие →", "stats_films", int64(end))})
	}
	return b.String(), keys, nil
}
func (h *Handler) statisticsSettingsPage(ctx context.Context, chat int64) (string, *models.InlineKeyboardMarkup, error) {
	sc, err := h.Statistics.Configuration(ctx, chat)
	if err != nil {
		return "", nil, err
	}
	if sc.Zone == "" {
		return "🗓 Недельная сводка\n\nСначала задайте часовой пояс чата через /timezone, например /timezone Asia/Novosibirsk.", statisticsNavigation(), nil
	}
	days := []string{"воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"}
	state, action := "выключена", "Включить"
	if sc.Enabled {
		state, action = "включена", "Приостановить"
	}
	text := fmt.Sprintf("🗓 Недельная сводка · %s\n\n%s, %s · %s\n\nДо трёх новых итогов за семь полных дней перед выпуском. Если содержательных результатов нет — бот ничего не отправляет.\n\nИзменения действуют со следующего будущего слота. Предпросмотр ничего не публикует.", state, days[sc.Weekday], sc.Clock, sc.Zone)
	button := func(label, action string) models.InlineKeyboardButton {
		return models.InlineKeyboardButton{Text: label, CallbackData: fmt.Sprintf("cp:stat:%s:%d", action, sc.Revision)}
	}
	keys := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{button(action, "toggle")},
		{button("День →", "day"), button("Час +1", "hour"), button("Минуты +15", "minute")},
		{profileButton("👀 Предпросмотр", "stats_preview", 0)},
		{profileButton("← Статистика", "stats", 0)},
	}}
	return text, keys, nil
}

const statisticsRules = "ℹ️ Как считаем\n\n• Используем только проверенные финальные ответы. Открытый опрос — не ноль голосов.\n• «Не смотрел» и «не могу оценить» исключены из оценок фильма.\n• Один человек мог ответить в нескольких опросах: число ответов не равно числу участников.\n• Повторы не суммируются: первый достаточный ответ на тему за просмотр, затем последний подходящий просмотр фильма. Минимум — 3 ответа в одном вопросе; это порог показа, а не доказательство точности.\n• «Мнения разделились»: минимум 5 оценок, минимум по 2 и по 30% положительных и отрицательных.\n• Интерес, удовольствие и правдоподобность считаем отдельно. Из ответов разных людей не придумываем причины.\n• Общие аспекты сравниваем только внутри совместимой шкалы, с равным весом фильмов. Для обобщения нужно хотя бы 3 фильма.\n• Лидер опроса — лидер среди показанных вариантов, не обязательно любимый фильм клуба.\n\nРасчёт выполняется кодом; ИИ не придумывает выводы."

func statisticsOverview(s clubprofile.StatisticsSnapshot) string {
	var b strings.Builder
	b.WriteString("🍿 Уже есть итоги")
	for _, c := range clubprofile.SelectStatisticsClaims(s.Claims, 2) {
		b.WriteString("\n\n" + c.Text)
	}
	if len(s.Claims) == 0 {
		b.WriteString("\nПока собираем первые оценки. Подробности — в статистике.")
	}
	return b.String()
}
