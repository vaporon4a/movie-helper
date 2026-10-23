package telegram

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (v clubProfileView) watchHeader(w clubprofile.WatchEvent) string {
	return fmt.Sprintf("🎞 %s\nПросмотр #%d · %s (%s)", clipProfile(w.Film.Label(), 180), w.ID, watchDate(w.Date), w.Zone)
}

const tmdbAttribution = "Данные: TMDB. This product uses the TMDB API but is not endorsed or certified by TMDB."

func (v clubProfileView) watchCard(id int64) (string, *models.InlineKeyboardMarkup) {
	index := slices.IndexFunc(v.watches, func(w clubprofile.WatchEvent) bool { return w.ID == id })
	if index < 0 {
		return "Просмотр не найден в этом чате.", profileNavigation()
	}
	w := v.watches[index]
	text := v.watchHeader(w) + "\n\n" + v.enjoymentLine(id) + "\n" + v.compactReviewStatus(w) + "\n\n" + tmdbAttribution
	keyboard := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{profileButton("💬 Впечатления", "impressions", id), profileButton("ℹ️ Подробнее", "details", id)},
		{profileButton("Другие действия…", "actions", id)},
		{profileButton("← Просмотры", "watches", int64((len(v.watches)-1-index)/5*5)), profileButton("🎯 Профиль", "overview", 0)},
	}}
	return text, keyboard
}
func (v clubProfileView) compactReviewStatus(w clubprofile.WatchEvent) string {
	if w.State != watchActive {
		return "Запись отменена; история и ответы сохранены."
	}
	for _, s := range v.sessions {
		if s.WatchID == w.ID {
			return "Обсуждение: " + sessionLabel(s.State, s.Reason) + "\n" + v.preparationLabel(s)
		}
	}
	return "Сохранён в историю; вопросы не запланированы."
}
func (v clubProfileView) preparationLabel(s clubprofile.ReviewSession) string {
	if s.State != "queued" && s.State != "researching" && s.State != "blocked" {
		return "Дальнейшие вопросы не запланированы; ответы сохранены."
	}
	if s.Plan.Version == clubprofile.CoverageV2 {
		return "Адресный план принят; вопросы по особенностям фильма."
	}
	for _, p := range v.preparations {
		if p.SessionID == s.ID {
			return p.Label(s)
		}
	}
	if len(v.watchPolls(s.WatchID)) > 0 {
		return "Идёт обычная серия вопросов."
	}
	return "Ожидает обсуждения; план будет выбран при первом вопросе."
}
func (v clubProfileView) watchSection(id int64, page string) (string, *models.InlineKeyboardMarkup) {
	index := slices.IndexFunc(v.watches, func(w clubprofile.WatchEvent) bool { return w.ID == id })
	if index < 0 {
		return v.watchCard(id)
	}
	w := v.watches[index]
	text := v.watchHeader(w) + "\n\n"
	keyboard := &models.InlineKeyboardMarkup{}
	switch page {
	case "details":
		var b strings.Builder
		b.WriteString(text)
		b.WriteString(v.reviewStatus(id))
		appendFilmMetadata(&b, w.Film)
		fmt.Fprintf(&b, "\nhttps://www.themoviedb.org/movie/%d", w.TMDBID)
		text = b.String()
	case "impressions":
		text += v.enjoymentLine(id)
		polls := v.watchPolls(id)
		if len(polls) == 0 {
			text += "\nОтветов пока нет."
		}
		for _, p := range polls {
			keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{profileButton(fmt.Sprintf("Опрос #%d · %s", p.ID, questionAspectLabel(p.Question)), "poll", p.ID)})
		}
	case "actions":
		text += "История и ответы сохраняются. Остановка вопросов требует подтверждения; исправление даты выполняется вручную."
		if w.State == watchActive {
			keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{{Text: "Остановить вопросы…", CallbackData: fmt.Sprintf("cp:pause:%d", id)}, {Text: "Исправить дату", CallbackData: fmt.Sprintf("cp:edit:%d", id)}})
		}
	}
	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{profileButton("← К фильму", watchPage, id)})
	return text, keyboard
}
