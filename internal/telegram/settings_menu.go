package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/daily"
)

func (h *Handler) settingsHome(ctx context.Context, chat int64) (menuPanel, error) {
	schedules, err := h.Daily.Schedules(ctx, chat)
	if err != nil {
		return menuPanel{}, err
	}
	zone := "не задан"
	lines := []string{"⚙️ Настройки киноклуба"}
	if len(schedules) > 0 && schedules[0].Zone != "" {
		zone = schedules[0].Zone
	}
	lines = append(lines, "🌍 Часовой пояс: "+zone, "")
	for _, s := range schedules {
		name := map[string]string{daily.Meme: "Мем", daily.Fact: "Факт"}[s.Kind]
		state := scheduleDisabledText
		if s.Enabled {
			state = scheduleEnabledText
		}
		lines = append(lines, fmt.Sprintf("%s · %s · %s", name, s.Clock, state))
	}
	if len(schedules) > 0 {
		lines = append(lines, "", moderationText(schedules[0].Moderation))
	}
	lines = append(lines, "", "Выберите раздел. Настройки доступны всем участникам клуба.")
	keyboard := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{menuButton("📅 Что дальше", "agenda_0")},
		{menuButton("🎬 Просмотры и опросы", "movies")},
		{menuButton("🗞 Мемы и факты", dailyPage)},
		{menuButton("🍿 Встречи и напоминания", "club")},
		{menuButton("💡 Идеи", "ideas")},
		{menuButton("🔧 Дополнительно", "advanced")},
		{menuButton("🌍 Изменить часовой пояс", "timezone")},
		{menuButton("🏠 Помощь", "help"), menuButton("ℹ️ О боте", "about")},
	}}
	return menuPanel{strings.Join(lines, "\n"), keyboard}, nil
}

func (h *Handler) dailyMenu(ctx context.Context, chat int64) (menuPanel, error) {
	schedules, err := h.Daily.Schedules(ctx, chat)
	if err != nil {
		return menuPanel{}, err
	}
	lines := []string{"🗞 Мемы и факты", ""}
	for _, s := range schedules {
		lines = append(lines, scheduleText(s))
	}
	keyboard := menuBack()
	if h.DailyMenu != nil {
		keyboard.InlineKeyboard = append([][]models.InlineKeyboardButton{{menuButton("🎭 Мем дня", "daily_meme"), menuButton("🎬 Факт дня", "daily_fact")}}, keyboard.InlineKeyboard...)
		lines = append(lines, "", "Выберите рубрику: время, включение и пример — кнопками ниже.")
	} else {
		lines = append(lines, "", "Попробовать сейчас:\n/preview meme\n/preview fact", "", "Настроить время и включить:\n/schedule meme 09:00\n/schedule fact 12:00", "", "Выключить рубрику:\n/pause meme\n/pause fact", "", "Время — по часовому поясу чата. Предпросмотр не меняет расписание.")
	}
	return menuPanel{strings.Join(lines, "\n"), keyboard}, nil
}

func (h *Handler) moviesMenu(ctx context.Context, chat int64) (menuPanel, error) {
	text := "🎬 Просмотры и опросы\n\nПрофиль временно недоступен."
	if h.Profile != nil {
		schedules, _, err := h.Profile.Settings(ctx, chat)
		if err != nil {
			return menuPanel{}, err
		}
		status, err := h.Profile.Status(ctx, chat)
		if err != nil {
			return menuPanel{}, err
		}
		state := scheduleDisabledText
		if status.AutoReview {
			state = scheduleEnabledText
		}
		text = profileSettingsText(schedules, nil) + "\n\nАвтоматические вопросы после просмотра: " + state
	}
	text += "\n\nДобавить просмотр:\n/movie_watched название или ссылка TMDB\nПосмотреть профиль: /movie_profile\n\nРасписание — день mon..sun и время чата:\n/movie_schedule genre mon 09:00\n/movie_schedule reference wed 12:00\n\nВыключить тип опросов:\n/movie_pause genre\n/movie_pause reference\n\nАвтовопросы для новых просмотров:\n/movie_auto_review on\n/movie_auto_review off\n\nЗапустить опрос сейчас:\n/genre_poll 10m\n/reference_poll 10m\n\nИзменения настроек не перезапускают остановленные серии."
	keyboard := menuBack()
	if h.WatchDialog != nil {
		keyboard.InlineKeyboard = append([][]models.InlineKeyboardButton{{{Text: "🎞 Добавить просмотр", CallbackData: "wd:start"}}}, keyboard.InlineKeyboard...)
	}
	if h.ScheduleUI != nil {
		keyboard.InlineKeyboard = append([][]models.InlineKeyboardButton{{menuButton("🗓 Жанры", "schedule_genre"), menuButton("🗓 Фильмы", "schedule_reference")}}, keyboard.InlineKeyboard...)
	}
	return menuPanel{text, keyboard}, nil
}

func (h *Handler) clubMenu(ctx context.Context, chat int64) (menuPanel, error) {
	text := "🍿 Напоминания временно недоступны."
	if h.Club != nil {
		settings, err := h.Club.Settings(ctx, chat)
		if err != nil {
			return menuPanel{}, err
		}
		text = clubSettingsText(settings, h.Now())
	}
	text += "\n\nНастроить встречи:\n/club_schedule fri 2 19:00\n\nПример: вторая пятница месяца; 19:00 — время напоминаний. Дни mon..sun, недели 1..4 или last.\n\nПопробовать текст: /club_preview\nВыключить: /club_pause"
	keyboard := menuBack()
	if h.ScheduleUI != nil {
		keyboard.InlineKeyboard = append([][]models.InlineKeyboardButton{{menuButton("🗓 Настроить встречи", "schedule_club")}}, keyboard.InlineKeyboard...)
	}
	return menuPanel{text, keyboard}, nil
}

func (h *Handler) ideasMenu(ctx context.Context, chat int64) (menuPanel, error) {
	text := "💡 Голосования за функции временно недоступны."
	if h.Features != nil {
		settings, err := h.Features.Settings(ctx, chat)
		if err != nil {
			return menuPanel{}, err
		}
		text = featureSettingsText(settings)
	}
	text += "\n\nПредложить улучшение: /idea\nВаши предложения: /my_ideas\n\nНастроить голосования:\n/idea_schedule sun 18:00\nВыключить: /idea_pause\n\nПробный запуск: /idea_round 10m\n\nУправление идеями по ID:\n/idea_remove ID — убрать\n/idea_done ID — отметить реализованной\n/idea_restore ID — вернуть\n\nИзменения идей в открытом голосовании ограничены, чтобы сохранить корректность результатов."
	keyboard := menuBack()
	keyboard.InlineKeyboard = append([][]models.InlineKeyboardButton{{menuButton("🧰 Бэклог победителей", "backlog")}}, keyboard.InlineKeyboard...)
	if h.ScheduleUI != nil {
		keyboard.InlineKeyboard = append([][]models.InlineKeyboardButton{{menuButton("🗓 Расписание идей", "schedule_ideas")}}, keyboard.InlineKeyboard...)
	}
	return menuPanel{text, keyboard}, nil
}

func (h *Handler) backlogMenu(ctx context.Context, chat int64) (menuPanel, error) {
	text := "💡 Бэклог временно недоступен."
	if h.Features != nil {
		ideas, err := h.Features.Backlog(ctx, chat)
		if err != nil {
			return menuPanel{}, err
		}
		text = featureIdeasText("🧰 Бэклог разработки", ideas)
	}
	// Large clubs can have a long backlog. The full legacy command stays available.
	if len([]rune(text)) > 3400 {
		text = clipProfile(text, 3400) + "\n\nПолный список: /idea_backlog"
	}
	return menuPanel{text, &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{menuButton("← Идеи", "ideas")}, {menuButton("⚙️ Настройки", "home")}}}}, nil
}

func (h *Handler) diagnosticsMenu(ctx context.Context, chat int64) (menuPanel, error) {
	now := h.Now()
	schedules, err := h.Daily.Schedules(ctx, chat)
	if err != nil {
		return menuPanel{}, err
	}
	issues, err := h.Daily.Issues(ctx, chat)
	if err != nil {
		return menuPanel{}, err
	}
	stocks, err := h.Daily.Stocks(ctx, chat, now)
	if err != nil {
		return menuPanel{}, err
	}
	text := "🛠 Диагностика\n\n" + settingsText(schedules, issues, now, stocks) + "\n\n" + diagnosticsHelp
	keyboard := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{menuButton("🎬 Состояние киноопросов", "movie_state")}, {menuButton("← Дополнительно", "advanced")}}}
	return menuPanel{text, keyboard}, nil
}

func (h *Handler) movieStateMenu(ctx context.Context, chat int64) (menuPanel, error) {
	text := "🎬 Киноопросы временно недоступны."
	if h.Profile != nil {
		schedules, polls, err := h.Profile.Settings(ctx, chat)
		if err != nil {
			return menuPanel{}, err
		}
		status, err := h.Profile.Status(ctx, chat)
		if err != nil {
			return menuPanel{}, err
		}
		text = profileSettingsText(schedules, polls) + profileStatusText(status)
	}
	return menuPanel{text, &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{menuButton("← Диагностика", "diagnostics")}}}}, nil
}

const moderationHelp = `📥 Ручная модерация

По умолчанию материалы отбирает AI. Ручную очередь можно включить отдельно:
/moderation on — ручное одобрение
/moderation off — автоматический отбор

/suggest_meme — отправьте ответом на одно фото
/suggest_fact Текст | https://источник — предложить факт
/queue — очередь; следующая страница: /queue последний_ID
/review ID — показать материал с кнопками одобрения
/approve ID — одобрить
/reject ID — убрать

Предложения участников публикуются только при включённой ручной очереди. Одобрять может любой участник клуба.`

const diagnosticsHelp = `/id — ID текущего чата
/refill fact|meme|titles — фоновое пополнение запасов

Восстановление доставки — только после проверки сообщений в чате:
/resolve ID sent — подтвердить отправку материала
/resolve ID requeue — вернуть материал на следующий день
/club_resolve ID sent|retry|cancel — напоминания
/movie_resolve ID retry|cancel — киноопросы
/idea_resolve ID sent|retry|cancel — голосования за функции
/resume — восстановить работу после потери доступа

Для исправления даты просмотра вручную:
/movie_watch_date ID YYYY-MM-DD
Это остановит дальнейшие вопросы серии; история и ответы сохранятся.`
