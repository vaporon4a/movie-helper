package telegram

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

const help = `🎬 Мем дня и интересный факт о кино — автоматически.

/settings — настройки и расписание
/preview meme — попробовать мем
/preview fact — попробовать факт
/timezone Europe/Moscow — часовой пояс
/schedule meme 09:00 — включить мем дня
/schedule fact 12:00 — включить факт дня
/pause meme или /pause fact — выключить рубрику
/genre_poll 10m — запустить пробный опрос жанров
/movie_schedule genre wed 19:00 — еженедельный киноопрос
/movie_pause genre [wed] — выключить киноопросы
/movie_settings — расписание и состояние киноопросов
/about — источники и правила подбора фильмов
/help_admin — модерация и служебные команды

Управление доступно администраторам. В новом чате задайте часовой пояс и включите расписание.`

const helpAdmin = `Модерация и служебные команды.

/id — ID текущего чата
/moderation on — включить ручное одобрение
/moderation off — вернуть автоматический отбор AI
/suggest_meme — ответьте этой командой на одно фото
/suggest_fact Текст факта | https://источник — предложить факт
/queue — очередь (следующая страница: /queue последний_ID)
/review ID — показать материал и кнопки одобрения
/approve ID или /reject ID — одобрить или убрать материал
/resolve ID sent — подтвердить неопределённую доставку
/resolve ID requeue — вернуть её материал на следующий день
/movie_resolve ID sent|retry|cancel — разрешить неопределённое состояние киноопроса
/resume — восстановить работу после потери доступа
/help — расписание и основные команды

Настройка, предпросмотр, очередь и одобрение доступны администраторам.
Предпросмотр использует дневные лимиты AI, не меняет расписание и не добавляет материал в очередь.
По умолчанию AI отбирает и одобряет мемы из Reddit и факты из Wikipedia для публикации по расписанию. Ручная очередь выключена.
/moderation on — включить очередь: новые материалы AI ждут /approve; публикуется только одобренное администратором.
/moderation off — автоматический отбор AI; предложения участников сохраняются, но не публикуются.
При сбое или отсутствии материала бот повторяет подбор до 6 раз в течение 6 часов, до конца дня. Рассылка изначально выключена.`

const about = `Подборки для киновечера

Бот запускает анонимный опрос из 10 жанров. Через 24 часа он останавливает голосование и публикует до 20 популярных фильмов победившего жанра. При равенстве могут учитываться два жанра.

Названия, описания, рейтинги и постеры получены через TMDB. This product uses the TMDB API but is not endorsed or certified by TMDB.

Недавно предложенные фильмы не повторяются 90 дней. Голоса хранятся только как итоговые числа по вариантам; данные о голосовавших не сохраняются.`

func moderationText(enabled bool) string {
	if enabled {
		return "Ручное одобрение включено: публикация только после /approve. Новые материалы AI сохраняются в /queue для следующего слота после одобрения."
	}
	return "Автоматический режим: AI отбирает и одобряет материалы. Ручная очередь выключена."
}

func settingsText(schedules []daily.Schedule, issues []daily.Delivery, now time.Time) string {
	lines := []string{"Расписание (по времени чата):"}
	if len(schedules) > 0 {
		lines = append(lines, moderationText(schedules[0].Moderation))
	}
	for _, schedule := range schedules {
		lines = append(lines, scheduleText(schedule))
	}
	for _, delivery := range issues {
		lines = append(lines, deliveryText(delivery, schedules, now))
	}
	lines = append(lines, "Источники: Reddit и Wikipedia. Автоматический отбор: Gemini с резервом Groq (если настроен).")
	for _, delivery := range issues {
		if delivery.State == "unknown" {
			lines = append(lines, "unknown: проверьте чат и выполните /resolve ID sent или /resolve ID requeue.")
			break
		}
	}
	return strings.Join(lines, "\n")
}

func scheduleText(schedule daily.Schedule) string {
	zone := schedule.Zone
	if zone == "" {
		zone = "не выбран — /timezone"
	}
	state := "выключено"
	if schedule.Enabled {
		state = "включено"
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
		lines = append(lines, "Очередь пуста. AI подбирает материалы во время включённого расписания. Можно предложить /suggest_fact или /suggest_meme.")
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
		return "Groq вернул ограничение квоты (429). Подождите минуту и проверьте лимиты в Groq Console.", "groq_quota"
	case "groq_unavailable":
		return "Groq временно недоступен (503). Попробуйте позже; попытка учтена в дневном лимите бота.", "groq_unavailable"
	case "gemini_model_unavailable":
		return "Выбранная модель Gemini недоступна для этого API-ключа. Нужно обновить GEMINI_MODEL в настройках бота.", "gemini_model_unavailable"
	case "gemini_access_denied":
		return "Gemini отклонил доступ. Проверьте API-ключ и разрешения проекта Google AI Studio.", "gemini_access_denied"
	case "gemini_quota":
		return "Gemini вернул ограничение квоты Google (429). Проверьте квоты проекта в Google AI Studio; внутренний лимит бота — отдельный.", "gemini_quota"
	case "gemini_unavailable":
		return "Gemini временно недоступен (503). Попробуйте позже; этот запрос учтён в дневном лимите бота.", "gemini_unavailable"
	case "groq_http":
		return "Запрос к Groq завершился ошибкой. Код HTTP: " + strconv.Itoa(problem.Status) + ".", "groq_http_" + strconv.Itoa(problem.Status)
	case "gemini_http":
		return "Запрос к Gemini завершился ошибкой. Код HTTP: " + strconv.Itoa(problem.Status) + ".", "gemini_http_" + strconv.Itoa(problem.Status)
	}
	return "Не удалось получить или обработать материал для предпросмотра. Попробуйте позже.", "source_or_generation_failed"
}
