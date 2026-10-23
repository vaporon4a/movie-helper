package telegram

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

const watchActive = "active"

type clubProfileView struct {
	watches   []clubprofile.WatchEvent
	sessions  []clubprofile.ReviewSession
	polls     []clubprofile.Poll
	schedules []clubprofile.Schedule
	now       time.Time
}

func (h *Handler) loadProfileView(ctx context.Context, chat int64) (clubProfileView, error) {
	v := clubProfileView{now: h.Now()}
	var err error
	v.watches, v.sessions, err = h.Watches.History(ctx, chat)
	if err != nil {
		return v, err
	}
	v.schedules, v.polls, err = h.Profile.Settings(ctx, chat)
	return v, err
}

func (h *Handler) showClubProfile(ctx context.Context, chat int64, message int, page string, id int64) error {
	v, err := h.loadProfileView(ctx, chat)
	if err != nil {
		return err
	}
	text, keyboard := v.page(page, id)
	keyboard = closableKeyboard(keyboard)
	if message == 0 {
		_, err = h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chat, Text: text, ReplyMarkup: keyboard, LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: new(true)}})
	} else {
		_, err = h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chat, MessageID: message, Text: text, ReplyMarkup: keyboard, LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: new(true)}})
	}
	if errors.Is(err, bot.ErrorBadRequest) && strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
		return errResponseSent
	}
	if err != nil {
		return err
	}
	return errResponseSent
}

func (v clubProfileView) page(page string, id int64) (string, *models.InlineKeyboardMarkup) {
	switch page {
	case "watches":
		return v.watchList(int(id))
	case "watch":
		return v.watchCard(id)
	case "pause", "edit":
		return v.watchActionCard(id, page)
	case "interest":
		return v.interestCard(int(id))
	case "poll":
		return v.pollCard(id)
	case "research":
		return v.researchCard(int(id))
	case "aspects":
		return v.aspectCard()
	default:
		return v.overviewCard()
	}
}

func profileNavigation() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{profileButton("🎯 Профиль", "overview", 0), profileButton("🎞 Просмотры", "watches", 0)},
		{profileButton("🔎 Аспекты", "aspects", 0), profileButton("🎭 Интересы", "interest", 0)},
	}}
}

func profileButton(label, page string, id int64) models.InlineKeyboardButton {
	return models.InlineKeyboardButton{Text: label, CallbackData: fmt.Sprintf("cp:view:%s:%d", page, id)}
}

func (v clubProfileView) overviewCard() (string, *models.InlineKeyboardMarkup) {
	p := clubprofile.ProjectProfile(v.watches, v.sessions, v.polls)
	var b strings.Builder
	fmt.Fprintf(&b, "🎯 Профиль киноклуба\n\nПосмотрели: %d разных фильмов · %d просмотров\nС оценками: %d фильмов\nОпросов об интересах: %d · исторических: %d\n", p.Films, p.Viewings, p.RatedFilms, p.InterestPolls, p.LegacyPolls)
	b.WriteString("\nПоследние просмотры:\n")
	n := 0
	for i := len(v.watches) - 1; i >= 0 && n < 3; i-- {
		w := v.watches[i]
		if w.State != watchActive {
			continue
		}
		fmt.Fprintf(&b, "• %s · %s\n  %s\n", clipProfile(w.Film.Label(), 90), watchDate(w.Date), v.enjoymentLine(w.ID))
		n++
	}
	if n == 0 {
		b.WriteString("Пока пусто. Добавьте фильм через /movie_watched со ссылкой TMDB или названием.\n")
	}
	b.WriteString("\nОтветы анонимны. Профиль описывает ответивших в опросах, а не всех участников клуба. Просмотр сам по себе не означает, что фильм понравился.")
	return b.String(), profileNavigation()
}

func (v clubProfileView) watchPolls(id int64) []clubprofile.Poll {
	session := int64(0)
	for _, s := range v.sessions {
		if s.WatchID == id {
			session = s.ID
			break
		}
	}
	var polls []clubprofile.Poll
	if session == 0 {
		return polls
	}
	for _, p := range v.polls {
		if p.SessionID == session {
			polls = append(polls, p)
		}
	}
	return polls
}

func (v clubProfileView) enjoymentLine(watch int64) string {
	polls := v.watchPolls(watch)
	for i := len(polls) - 1; i >= 0; i-- {
		p := polls[i]
		if p.Question.Intent != clubprofile.Enjoyment || p.Result == nil || p.State != clubprofile.Completed || len(p.Result.Counts) != 7 {
			continue
		}
		n := (clubprofile.Observation{Question: p.Question, Result: *p.Result}).Answers()
		if n == 0 {
			return "Содержательных оценок пока нет"
		}
		return fmt.Sprintf("Положительно: %d из %d оценивших; без оценки: %d", p.Result.Counts[3]+p.Result.Counts[4], n, p.Result.Counts[5]+p.Result.Counts[6])
	}
	return "Оценок пока нет"
}

func (v clubProfileView) watchList(offset int) (string, *models.InlineKeyboardMarkup) {
	if offset < 0 || offset > len(v.watches) {
		offset = 0
	}
	keyboard := profileNavigation()
	var b strings.Builder
	b.WriteString("🎞 История киноклуба\n\n")
	end := min(offset+5, len(v.watches))
	for j := offset; j < end; j++ {
		w := v.watches[len(v.watches)-1-j]
		state := ""
		if w.State != watchActive {
			state = " · отменён"
		}
		fmt.Fprintf(&b, "#%d · %s\n%s%s\n\n", w.ID, clipProfile(w.Film.Label(), 100), watchDate(w.Date), state)
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{profileButton(fmt.Sprintf("#%d · Подробнее", w.ID), "watch", w.ID)})
	}
	if len(v.watches) == 0 {
		b.WriteString("Добавьте просмотр: /movie_watched название или ссылка TMDB")
	}
	if end < len(v.watches) {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{profileButton("Следующие 5", "watches", int64(end))})
	}
	return b.String(), keyboard
}

func (v clubProfileView) watchCard(id int64) (string, *models.InlineKeyboardMarkup) {
	keyboard := profileNavigation()
	index := slices.IndexFunc(v.watches, func(w clubprofile.WatchEvent) bool { return w.ID == id })
	if index < 0 {
		return "Просмотр не найден в этом чате.", keyboard
	}
	w := v.watches[index]
	var b strings.Builder
	fmt.Fprintf(&b, "🎞 %s\nПросмотр #%d · %s (%s)\nhttps://www.themoviedb.org/movie/%d\n\n%s\n", clipProfile(w.Film.Label(), 180), w.ID, watchDate(w.Date), w.Zone, w.TMDBID, v.enjoymentLine(w.ID))
	if w.State != watchActive {
		b.WriteString("\nЗапись отменена. История и ответы сохранены, из сводного профиля исключены.\n")
	} else {
		b.WriteString(v.reviewStatus(w.ID))
	}
	appendFilmMetadata(&b, w.Film)
	for _, p := range v.watchPolls(id) {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{profileButton(fmt.Sprintf("Опрос #%d · %s", p.ID, questionAspectLabel(p.Question)), "poll", p.ID)})
	}
	if w.State == watchActive {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{
			{Text: "Остановить вопросы…", CallbackData: fmt.Sprintf("cp:pause:%d", id)},
			{Text: "Исправить", CallbackData: fmt.Sprintf("cp:edit:%d", id)},
		})
	}
	return b.String(), keyboard
}

func (v clubProfileView) watchActionCard(id int64, action string) (string, *models.InlineKeyboardMarkup) {
	index := slices.IndexFunc(v.watches, func(w clubprofile.WatchEvent) bool { return w.ID == id })
	if index < 0 || v.watches[index].State != watchActive {
		return v.watchCard(id)
	}
	w := v.watches[index]
	keyboard := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{profileButton("← Назад к фильму", "watch", id)},
	}}
	heading := fmt.Sprintf("🎞 %s · просмотр #%d\n\n", clipProfile(w.Film.Label(), 180), id)
	if action == "edit" {
		return heading + fmt.Sprintf("Исправить дату вручную:\n/movie_watch_date %d YYYY-MM-DD\n\nВнимание: исправление даты остановит дальнейшие вопросы этой серии. История и ответы сохранятся.\n\nОтмена просмотра кнопкой отключена. Если ошиблись фильмом, попросите исправить запись вручную.", id), keyboard
	}
	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{
		{Text: "Да, остановить вопросы", CallbackData: fmt.Sprintf("cp:pause_confirm:%d", id)},
	})
	return heading + "Остановить дальнейшие вопросы об этом просмотре?\n\nФильм останется в истории, собранные ответы сохранятся. Уже опубликованные опросы останутся до завершения. Серия сама не возобновится.\n\nЕсли просто открыли карточку — нажмите «Назад к фильму».", keyboard
}

func appendFilmMetadata(b *strings.Builder, f clubprofile.Film) {
	if len(f.Metadata.Genres) > 0 {
		names := make([]string, 0, len(f.Metadata.Genres))
		for _, g := range f.Metadata.Genres {
			names = append(names, g.Name)
		}
		b.WriteString("\nЖанры: " + clipProfile(strings.Join(names, ", "), 200))
	}
	if f.Metadata.Runtime != nil && *f.Metadata.Runtime > 0 {
		fmt.Fprintf(b, "\nДлительность: %d мин.", *f.Metadata.Runtime)
	}
	if len(f.Metadata.Countries) > 0 {
		b.WriteString("\nСтраны производства: " + clipProfile(strings.Join(f.Metadata.Countries, ", "), 120))
	}
	if f.Metadata.OriginalLanguage != "" {
		b.WriteString("\nЯзык оригинала: " + clipProfile(f.Metadata.OriginalLanguage, 20))
	}
	b.WriteString("\nДанные: TMDB. Это сведения о фильме, не предпочтения клуба.\nThis product uses the TMDB API but is not endorsed or certified by TMDB.")
}

func (v clubProfileView) reviewStatus(watch int64) string {
	for _, s := range v.sessions {
		if s.WatchID != watch {
			continue
		}
		polls := v.watchPolls(watch)
		d := clubprofile.ReviewProgress(s, polls, v.now)
		covered := len(d.Coverage.Covered)
		if d.Coverage.Overall {
			covered++
		}
		status := sessionLabel(s.State, s.Reason)
		return fmt.Sprintf("\nИсследование: %s\nПокрыто: %d/5 тем · минимум %d ответа на каждую в одном опросе\n%s\n%s\n", status, covered, s.Plan.MinimumAnswers, researchCoverageText(s.Plan, d.Coverage), v.nextResearchText(s))
	}
	return "\nСохранён как история; автоматическое обсуждение не запущено. Это бывает при старой/неточной дате или выключенном автообсуждении.\n"
}

func coverageText(c clubprofile.Coverage) string {
	var b strings.Builder
	for _, a := range []clubprofile.Aspect{clubprofile.Overall, clubprofile.Pace, clubprofile.Engagement, clubprofile.Characters, clubprofile.Visuals} {
		covered := c.Covered[a]
		if a == clubprofile.Overall {
			covered = c.Overall
		}
		mark := "▫️"
		if covered {
			mark = "✓"
		}
		fmt.Fprintf(&b, "%s %s\n", mark, aspectLabel(a))
	}
	return b.String()
}

func sessionLabel(state, reason string) string {
	switch state {
	case "completed_sufficient":
		return "данных достаточно по всем пяти темам"
	case "partial":
		return "завершено с неполными данными (" + reviewReason(reason) + ")"
	case "expired":
		return "не началось в срок"
	case "cancelled":
		return "остановлено участником; ответы сохранены"
	case "blocked":
		return "нужна проверка доставки или итогов"
	case "researching":
		return "идёт сбор ответов"
	default:
		return "в очереди"
	}
}

func reviewReason(reason string) string {
	switch reason {
	case "deadline":
		return "истёк срок обсуждения"
	case "question_budget":
		return "достигнут предел вопросов"
	case "two_empty_batches":
		return "два раза подряд не было оценок"
	case "insufficient_responses":
		return "не хватило ответов после повторов"
	case "question_cancelled":
		return "вопрос отменён"
	default:
		return "не все темы оценены"
	}
}

func (v clubProfileView) nextResearchText(s clubprofile.ReviewSession) string {
	if s.State != "queued" && s.State != "researching" {
		return "Новые вопросы не запланированы."
	}
	if reason := v.reviewWaitingReason(s); reason != "" {
		return reason
	}
	next, zone := v.nextResearchSlot()
	if next.IsZero() {
		return "Автоматическое обсуждение не запланировано: расписание выключено или нет свободного исследовательского слота. /movie_schedule"
	}
	if !s.FinishBefore.IsZero() && !next.Before(s.FinishBefore) {
		return "Ближайший слот позже срока обсуждения: 28 дней с первого вопроса. Изменить расписание: /movie_schedule"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return "Проверьте часовой пояс расписания."
	}
	return "Ближайший возможный исследовательский слот: " + next.In(loc).Format("02.01 15:04") + " · " + zone + ". До двух вопросов за слот."
}

func (v clubProfileView) pollCard(id int64) (string, *models.InlineKeyboardMarkup) {
	for _, p := range v.polls {
		if p.ID != id {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "🔎 Основание · опрос #%d\n%s\n", id, p.Question.Text)
		if p.Result == nil {
			fmt.Fprintf(&b, "\nРезультат пока неизвестен. Состояние: %s", p.State)
		} else {
			appendProfileResult(&b, p)
		}
		fmt.Fprintf(&b, "\n\nВерсия: %s · шкала: %s\nИсточник вопроса: %s", p.Question.Version, p.Question.Scale, p.Question.Source)
		return b.String(), profileNavigation()
	}
	return "Опрос не найден в этом чате.", profileNavigation()
}

func (v clubProfileView) interestCard(offset int) (string, *models.InlineKeyboardMarkup) {
	var polls []clubprofile.Poll
	for _, p := range v.polls {
		if p.State == clubprofile.Completed && (p.Question.Intent == clubprofile.Interest || p.Question.Intent == clubprofile.LegacyInterest) {
			polls = append(polls, p)
		}
	}
	keyboard := profileNavigation()
	if offset < 0 || offset >= len(polls) {
		offset = 0
	}
	if len(polls) == 0 {
		return "🎭 Интересы\nПока нет завершённых обычных опросов.", keyboard
	}
	p := polls[len(polls)-1-offset]
	var b strings.Builder
	b.WriteString("🎭 Интересы в опросах\nЭто не оценки после просмотра.\n")
	appendProfileResult(&b, p)
	if offset+1 < len(polls) {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{profileButton("Предыдущий опрос", "interest", int64(offset+1))})
	}
	return b.String(), keyboard
}

func (v clubProfileView) aspectCard() (string, *models.InlineKeyboardMarkup) {
	p := clubprofile.ProjectProfile(v.watches, v.sessions, v.polls)
	var b strings.Builder
	b.WriteString("🔎 Впечатления по аспектам\n\n")
	for _, a := range p.Aspects {
		fmt.Fprintf(&b, "%s · %d фильмов, %d вопросов, %d содержательных ответов\n", aspectLabel(a.Aspect), a.Films, a.Questions, a.Answers)
		if a.Films < 3 {
			b.WriteString("Пока мало фильмов для общего наблюдения.\n\n")
			continue
		}
		keys := make([]string, 0, len(a.Shares))
		for key := range a.Shares {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "• %s — в среднем %.0f%% оценок\n", a.Labels[key], 100*a.Shares[key])
		}
		b.WriteString("\n")
	}
	if len(p.Aspects) == 0 {
		b.WriteString("Пока нет оценок аспектов просмотренного кино.\n\n")
	}
	b.WriteString("Каждый фильм имеет равный вес. Это средние доли ответов в этих просмотрах, не доля участников клуба. Ответы разных вопросов нельзя связывать с одними и теми же людьми.")
	keyboard := profileNavigation()
	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{profileButton("🎬 Особенности фильмов", "research", 0)})
	return b.String(), keyboard
}

func aspectLabel(a clubprofile.Aspect) string {
	switch a {
	case clubprofile.Overall:
		return "Общее впечатление"
	case clubprofile.Pace:
		return "Темп"
	case clubprofile.Engagement:
		return "Вовлечённость"
	case clubprofile.Characters:
		return "Герои"
	case clubprofile.Visuals:
		return "Визуальное впечатление"
	default:
		return string(a)
	}
}

func (v clubProfileView) nextResearchSlot() (time.Time, string) {
	var next time.Time
	var zone string
	for _, sc := range v.schedules {
		if !sc.Enabled {
			continue
		}
		for day := 0; day < 8; day++ {
			slot, err := clubprofile.WeeklySlot(v.now.AddDate(0, 0, day), sc.Weekday, sc.Clock, sc.Zone)
			if err == nil && clubprofile.ResearchSlot(v.schedules, sc, slot) && slot.After(v.now) && slot.After(sc.Effective) && (next.IsZero() || slot.Before(next)) {
				next = slot
				zone = sc.Zone
			}
		}
	}
	return next, zone
}

func (v clubProfileView) reviewWaitingReason(s clubprofile.ReviewSession) string {
	for _, p := range v.watchPolls(s.WatchID) {
		if p.State == clubprofile.Open || p.State == clubprofile.Closing || p.State == clubprofile.Planned || p.State == clubprofile.Creating {
			return "Сначала дождёмся итогов уже подготовленных вопросов."
		}
	}
	for _, other := range v.sessions {
		if other.ID != s.ID && (other.State == "researching" || other.State == "blocked" || (other.State == "queued" && other.ID < s.ID)) {
			return "Перед этим фильмом есть другие просмотры в очереди."
		}
	}
	return ""
}
