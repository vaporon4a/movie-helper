package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (h *Handler) statisticsArchivePage(ctx context.Context, chat int64, page string, id int64) (string, *models.InlineKeyboardMarkup, error) {
	reports, err := h.Statistics.History(ctx, chat)
	keys := statisticsNavigation()
	if err != nil {
		return "", keys, err
	}
	if page == "stats_report" || page == "stats_proof" {
		reportID := id
		if page == "stats_proof" {
			reportID = id / 10
		}
		for _, r := range reports {
			if r.ID == reportID {
				return statisticsReportPage(r, keys, page, int(id%10))
			}
		}
		return "Выпуск не найден среди последних 20 сводок этого чата.", keys, nil
	}
	var b strings.Builder
	b.WriteString("🗂 Последние сводки\n\n")
	for _, r := range reports {
		date := r.Snapshot.Period.End.Format("02.01.2006")
		fmt.Fprintf(&b, "• %s · %s\n", date, statisticsDeliveryLabel(r.State))
		keys.InlineKeyboard = append(keys.InlineKeyboard, []models.InlineKeyboardButton{profileButton(date+" · Подробнее", "stats_report", r.ID)})
	}
	if len(reports) == 0 {
		b.WriteString("Выпусков пока нет. Предпросмотр доступен в настройках сводки.")
	}
	return b.String(), keys, nil
}
func statisticsReportPage(r clubprofile.StatisticsReport, keys *models.InlineKeyboardMarkup, page string, index int) (string, *models.InlineKeyboardMarkup, error) {
	keys.InlineKeyboard = append(keys.InlineKeyboard, []models.InlineKeyboardButton{profileButton("← Выпуски", "stats_archive", 0)})
	if page == "stats_proof" {
		if index < 0 || index >= len(r.Snapshot.Claims) {
			return "Основание не найдено.", keys, nil
		}
		c := r.Snapshot.Claims[index]
		var b strings.Builder
		fmt.Fprintf(&b, "🔎 Основание сохранённого выпуска\nОпрос #%d\n\n%s\n\n", c.PollID, c.Question.Text)
		for i, o := range c.Question.Options {
			if i < len(c.Counts) {
				fmt.Fprintf(&b, "• %s — %d\n", o.Text, c.Counts[i])
			}
		}
		b.WriteString("\nЭто снимок на момент подготовки выпуска; последующие исправления не меняют его.")
		return b.String(), keys, nil
	}
	for i := range r.Snapshot.Claims {
		keys.InlineKeyboard = append(keys.InlineKeyboard, []models.InlineKeyboardButton{profileButton(fmt.Sprintf("Основание %d", i+1), "stats_proof", r.ID*10+int64(i))})
	}
	text := "🗂 " + statisticsDeliveryLabel(r.State) + "\n\n" + r.Text
	if r.State == deliveryUnknown {
		text += "\n\nОтправка могла состояться. Автоматический повтор остановлен, чтобы не продублировать пост. Нужна ручная проверка."
	}
	if r.State == "failed" {
		text += "\n\nTelegram отклонил отправку. Нужна проверка доступа бота; повтор автоматически не выполняется."
	}
	return text, keys, nil
}
func statisticsDeliveryLabel(state string) string {
	switch state {
	case "sent":
		return "опубликована"
	case "pending", "retry", "sending":
		return "ожидает доставки"
	case "skipped":
		return "пропущена: нет новых итогов"
	case deliveryUnknown:
		return "нужно проверить доставку"
	case "failed":
		return "ошибка отправки"
	case "cancelled":
		return "остановлена настройками"
	case "expired":
		return "окно отправки прошло"
	default:
		return "статус уточняется"
	}
}
