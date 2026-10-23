package telegram

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

const scheduleDisabledText = "выключено"
const scheduleEnabledText = "включено"

const help = `🎬 Киноклуб

Записываем просмотренные фильмы, собираем впечатления и узнаём вкусы клуба. Бот также публикует мемы, факты и напоминает о выборе фильма.

/movie_watched — добавить просмотренный фильм
/movie_profile — история, впечатления и интересы клуба
/idea — предложить улучшение бота
/my_ideas — ваши активные предложения
/settings — расписания и настройки

Все участники клуба имеют одинаковые права.`

const helpManage = `🔧 Дополнительно

Ручная модерация — предложения участников и одобрение публикаций.
Диагностика — состояние запасов, доставки и команды восстановления.

Настройки доступны всем участникам клуба.`

const about = `🎬 Киноклуб: фильмы выбираем вместе

Анонимные опросы собирают интересы клуба и впечатления после просмотров. Итоги сохраняются без подборок и постеров: /movie_profile. Добавить просмотр: /movie_watched название или ссылка TMDB.

Все участники клуба равны. Данные разных чатов изолированы; Telegram ID голосовавших не сохраняются. Материалы ежедневных рубрик и предложения через /idea работают независимо.

Источники: мемы — Reddit; факты — русская Wikipedia с указанием источника и лицензии. Данные о фильмах — TMDB. AI помогает отбирать материалы и готовить вопросы.

This product uses the TMDB API but is not endorsed or certified by TMDB.`

func moderationText(enabled bool) string {
	if enabled {
		return "Ручное одобрение включено: публикация только после /approve. Новые материалы AI сохраняются в /queue для следующего слота после одобрения."
	}
	return "Автоматический режим: AI отбирает и одобряет материалы. Ручная очередь выключена."
}

func settingsText(schedules []daily.Schedule, issues []daily.Delivery, now time.Time, stockSets ...[]daily.Stock) string {
	lines := []string{"Расписание (по времени чата):"}
	if len(schedules) > 0 {
		lines = append(lines, moderationText(schedules[0].Moderation))
	}
	for _, schedule := range schedules {
		lines = append(lines, scheduleText(schedule))
	}
	if len(stockSets) > 0 {
		lines = append(lines, stockStatusLines(stockSets[0], scheduleZone(schedules), now)...)
	}
	for _, delivery := range issues {
		lines = append(lines, deliveryText(delivery, schedules, now))
	}
	lines = append(lines, "Источники: Reddit; фильмы TMDB → русская Wikipedia. AI отбирает факты без пересказа: Gemini с резервом Groq (если настроен).")
	for _, delivery := range issues {
		if delivery.State == deliveryUnknown {
			lines = append(lines, "unknown: проверьте чат и выполните /resolve ID sent или /resolve ID requeue.")
			break
		}
	}
	return strings.Join(lines, "\n")
}

func scheduleZone(schedules []daily.Schedule) *time.Location {
	if len(schedules) > 0 {
		if location, err := time.LoadLocation(schedules[0].Zone); err == nil {
			return location
		}
	}
	return time.UTC
}

func stockStatusLines(stocks []daily.Stock, zone *time.Location, now time.Time) []string {
	lines := make([]string, 0, len(stocks))
	for _, stock := range stocks {
		label := map[string]string{daily.Fact: "фактов", daily.Meme: "мемов"}[stock.Kind]
		if label == "" {
			label = stock.Kind
		}
		line := fmt.Sprintf("Запас %s: %d/%d", label, stock.Count, stock.Target)
		if stock.NextAttempt > now.Unix() {
			line += "; следующая попытка " + time.Unix(stock.NextAttempt, 0).In(zone).Format("15:04 MST")
		}
		if stock.LastError != "" {
			line += "; причина: " + preparationErrorText(stock.LastError)
		}
		lines = append(lines, line)
	}
	return lines
}

func scheduleText(schedule daily.Schedule) string {
	zone := schedule.Zone
	if zone == "" {
		zone = "не выбран — /timezone"
	}
	state := scheduleDisabledText
	if schedule.Enabled {
		state = scheduleEnabledText
	}
	return fmt.Sprintf("%s: %s, %s, %s", schedule.Kind, schedule.Clock, zone, state)
}

func deliveryText(delivery daily.Delivery, schedules []daily.Schedule, now time.Time) string {
	if delivery.State == "skipped" {
		if delivery.Error == "manual_approval_required" {
			return fmt.Sprintf("Подбор #%d: %s, материалы сохранены и ожидают ручного одобрения", delivery.ID, delivery.Kind)
		}
		return fmt.Sprintf("Подбор #%d: %s, пропущен после %d/%d попыток; причина: %s", delivery.ID, delivery.Kind, delivery.FetchAttempts, daily.MaxPreparationAttempts, preparationErrorText(delivery.Error))
	}
	if delivery.State != "preparing" {
		return fmt.Sprintf("Доставка #%d: %s, %s, %s", delivery.ID, delivery.Kind, delivery.Date, delivery.State)
	}
	when := "ожидается подбор"
	if delivery.NextAttempt > now.Unix() {
		zone := time.UTC
		if len(schedules) > 0 {
			if location, err := time.LoadLocation(schedules[0].Zone); err == nil {
				zone = location
			}
		}
		when = "следующая попытка в " + time.Unix(delivery.NextAttempt, 0).In(zone).Format("15:04 MST")
	}
	line := fmt.Sprintf("Подбор #%d: %s, попыток %d/%d; %s", delivery.ID, delivery.Kind, delivery.FetchAttempts, daily.MaxPreparationAttempts, when)
	if delivery.Error != "" {
		line += "; причина: " + preparationErrorText(delivery.Error)
	}
	return line
}

func preparationErrorText(code string) string {
	if strings.HasPrefix(code, "rejected:") || strings.HasPrefix(code, "validation_rejected:") {
		return "AI не одобрил проверенный материал"
	}
	switch code {
	case "no_approved_candidate":
		return "AI не одобрил ни одного кандидата"
	case "manual_approval_required":
		return "материалы ожидают ручного одобрения"
	case "provider_disabled":
		return "источник или AI не настроен"
	case "local_daily_limit":
		return "исчерпан дневной лимит AI"
	case "gemini_unavailable", "groq_unavailable":
		return "AI временно недоступен"
	case "gemini_quota", "groq_quota":
		return "исчерпана квота AI"
	case "gemini_access_denied", "groq_access_denied":
		return "AI отклонил ключ доступа"
	case "gemini_model_unavailable", "groq_model_unavailable":
		return "указанная AI-модель недоступна"
	case "invalid_ai_selection":
		return "AI вернул неполный или противоречивый результат"
	case "preparation_window_exhausted":
		return "закончилось окно повторных попыток"
	case "source_unavailable":
		return "источник временно недоступен"
	case "background_refill_pending", "stock_below_target":
		return "идёт фоновая подготовка"
	case "duplicate_candidates":
		return "источник вернул уже известные материалы"
	case "title_generation_failed":
		return "AI не подготовил название идеи"
	case "":
		return "подходящий материал не найден"
	default:
		return "внутренняя ошибка подготовки материала"
	}
}

func queueText(items []daily.Item) string {
	lines := []string{"Очередь материалов (ручное одобрение: /moderation on; автоматический режим: /moderation off):"}
	for _, item := range items {
		title := []rune(item.Text)
		if len(title) > 70 {
			title = title[:70]
		}
		lines = append(lines, fmt.Sprintf("#%d %s [%s] %s", item.ID, item.Kind, item.State, string(title)))
	}
	if len(items) == 0 {
		lines = append(lines, "Очередь пуста. AI готовит запас материалов в фоне. Можно предложить /suggest_fact или /suggest_meme.")
	}
	if len(items) == 10 {
		lines = append(lines, fmt.Sprintf("Дальше: /queue %d", items[9].ID))
	}
	return strings.Join(lines, "\n")
}

func previewError(err error) (string, string) {
	problem, ok := errors.AsType[*daily.PreviewError](err)
	if !ok {
		return "Не удалось получить или обработать материал для предпросмотра. Попробуйте позже.", "source_or_generation_failed"
	}
	switch problem.Code {
	case "invalid_ai_selection":
		return "AI вернул неполный или противоречивый результат проверки. Материал не опубликован. Попробуйте ещё раз.", "invalid_ai_selection"
	case "local_daily_limit":
		return "Подбор остановлен: дневной лимит AI-провайдера исчерпан или отключён. Счётчики обновятся в 00:00 UTC; предпросмотр и рубрики используют одни и те же лимиты.", "local_daily_limit"
	case "groq_access_denied":
		return "Groq отклонил доступ. Проверьте GROQ_API_KEY и разрешения проекта.", "groq_access_denied"
	case "groq_model_unavailable":
		return "Выбранная модель Groq недоступна. Проверьте GROQ_MODEL.", "groq_model_unavailable"
	case "groq_quota":
		return "Groq временно ограничил запросы (429). Повторите предпросмотр " + retryAfterText(problem.After) + "; публикация по расписанию повторится автоматически.", "groq_quota"
	case "groq_unavailable":
		return "Groq временно недоступен (503). Попробуйте позже; попытка учтена в дневном лимите бота.", "groq_unavailable"
	case "gemini_model_unavailable":
		return "Выбранная модель Gemini недоступна для этого API-ключа. Нужно обновить GEMINI_MODEL в настройках бота.", "gemini_model_unavailable"
	case "gemini_access_denied":
		return "Gemini отклонил доступ. Проверьте API-ключ и разрешения проекта Google AI Studio.", "gemini_access_denied"
	case "gemini_quota":
		return "Gemini временно ограничил запросы (429). Повторите предпросмотр " + retryAfterText(problem.After) + "; публикация по расписанию повторится автоматически.", "gemini_quota"
	case "gemini_unavailable":
		return "Gemini временно недоступен (503). Повторите предпросмотр " + retryAfterText(problem.After) + "; публикация по расписанию повторится автоматически.", "gemini_unavailable"
	case "groq_http":
		return "Запрос к Groq завершился ошибкой. Код HTTP: " + strconv.Itoa(problem.Status) + ".", "groq_http_" + strconv.Itoa(problem.Status)
	case "gemini_http":
		return "Запрос к Gemini завершился ошибкой. Код HTTP: " + strconv.Itoa(problem.Status) + ".", "gemini_http_" + strconv.Itoa(problem.Status)
	}
	return "Не удалось получить или обработать материал для предпросмотра. Попробуйте позже.", "source_or_generation_failed"
}

func retryAfterText(delay time.Duration) string {
	if delay <= 0 {
		return "через несколько минут"
	}
	if delay < time.Minute {
		seconds := max(1, int((delay+time.Second-1)/time.Second))
		return "примерно через " + strconv.Itoa(seconds) + " сек."
	}
	minutes := max(1, int((delay+time.Minute-1)/time.Minute))
	return "примерно через " + strconv.Itoa(minutes) + " мин."
}
