package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/agenda"
)

func (h *Handler) agendaPanel(ctx context.Context, chat int64, offset int) (menuPanel, error) {
	if h.Agenda == nil {
		return menuPanel{"Календарь временно недоступен.", menuBack()}, nil
	}
	v := h.Agenda.Read(ctx, chat, h.Now())
	var b strings.Builder
	b.WriteString("📅 Что дальше · ближайшие 7 дней\n\n")
	if v.Meeting != "" {
		b.WriteString("🍿 Следующая расчётная встреча: " + v.Meeting + "\n\n")
	}
	if offset < 0 || offset >= len(v.Events) {
		offset = 0
	}
	end := min(offset+10, len(v.Events))
	for _, e := range v.Events[offset:end] {
		b.WriteString(agendaEventText(e, v.Zone))
	}
	if len(v.Events) == 0 {
		b.WriteString("Ближайших событий не запланировано.\n")
	}
	if len(v.Queue) > 0 {
		b.WriteString("🎞 Очередь обсуждения (окно не обещает конкретный фильм):\n")
		for _, film := range v.Queue[:min(5, len(v.Queue))] {
			b.WriteString("• " + clipProfile(film, 130) + "\n")
		}
	}
	if len(v.Unavailable) > 0 {
		b.WriteString("\nНе удалось прочитать: " + strings.Join(v.Unavailable, ", ") + ". Показаны доступные данные.")
	}
	b.WriteString("\nРасчётные события зависят от готовности материалов и доступа. Просмотр календаря ничего не запускает.")
	k := menuBack()
	if end < len(v.Events) {
		k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{menuButton("Дальше →", "agenda_"+strconv.Itoa(end))})
	}
	if offset > 0 {
		k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{menuButton("← Назад", "agenda_"+strconv.Itoa(max(0, offset-10)))})
	}
	k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{menuButton("↻ Обновить", "agenda_"+strconv.Itoa(offset)), menuButton("🛠 Проблемы", "recovery")})
	return menuPanel{limitMenuText(b.String()), k}, nil
}

func agendaEventText(e agenda.Event, defaultZone string) string {
	zone := e.Zone
	if zone == "" {
		zone = defaultZone
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	return fmt.Sprintf("%s (%s) · %s\n%s\n\n", e.At.In(loc).Format("02.01 15:04"), loc, e.Label, e.Status)
}
