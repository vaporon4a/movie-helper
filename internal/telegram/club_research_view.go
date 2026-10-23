package telegram

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (v clubProfileView) researchCard(offset int) (string, *models.InlineKeyboardMarkup) {
	summary := clubprofile.ProjectResearch(v.watches, v.sessions, v.polls)
	keyboard := profileNavigation()
	var b strings.Builder
	b.WriteString("🎬 Особенности просмотренных фильмов\n\n")
	if offset < 0 || offset >= len(summary.Families) {
		offset = 0
	}
	end := min(offset+3, len(summary.Families))
	for _, f := range summary.Families[offset:end] {
		b.WriteString(researchGroupHeading(f))
		fmt.Fprintf(&b, "%s\n%d фильмов · %d содержательных ответов\n", clubprofile.ResearchFamilyLabel(f.Family), f.Films, f.Answers)
		if f.Films < 3 {
			b.WriteString("Пока мало фильмов для общего вывода.\n\n")
			continue
		}
		keys := make([]string, 0, len(f.Shares))
		for key := range f.Shares {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "• %s: %.0f%%\n", f.Labels[key], 100*f.Shares[key])
		}
		b.WriteString("\n")
	}
	if len(summary.Families) == 0 {
		b.WriteString("Пока нет достаточно полных ответов на адресные вопросы.\n\n")
	}
	fmt.Fprintf(&b, "Неполных наблюдений: %d. Несовместимых: %d.\n", summary.Provisional, summary.Quarantined)
	b.WriteString("Это реакции на особенности конкретных фильмов. Каждый фильм весит одинаково; повторные вопросы не добавляют ему веса. Проценты — средние доли ответов, не доли участников клуба. Интерес, удовольствие и правдоподобность считаются отдельно.")
	if end < len(summary.Families) {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{profileButton("Дальше →", "research", int64(end))})
	}
	if offset > 0 {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{profileButton("← Назад", "research", int64(max(0, offset-3)))})
	}
	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{profileButton("🔎 На чём основано", "evidence", 0)})
	return b.String(), keyboard
}

func questionAspectLabel(q clubprofile.Question) string {
	if q.Measurement != nil {
		return clubprofile.ResearchFamilyLabel(q.Measurement.FamilyID)
	}
	return aspectLabel(q.Aspect)
}
func researchCoverageText(plan clubprofile.ReviewPlan, c clubprofile.Coverage) string {
	if plan.Version != clubprofile.CoverageV2 {
		return coverageText(c)
	}
	var b strings.Builder
	mark := "▫️"
	if c.Overall {
		mark = "✓"
	}
	fmt.Fprintf(&b, "%s Общее впечатление\n", mark)
	for _, q := range plan.Questions {
		mark = "▫️"
		if c.Covered[q.PlanTarget()] {
			mark = "✓"
		}
		fmt.Fprintf(&b, "%s %s\n", mark, questionAspectLabel(q))
	}
	return b.String()
}

func (v clubProfileView) evidenceCard(offset int) (string, *models.InlineKeyboardMarkup) {
	summary := clubprofile.ProjectResearch(v.watches, v.sessions, v.polls)
	k := profileNavigation()
	var b strings.Builder
	b.WriteString("🔎 Основания профиля\n\nПоказаны сохранённые результаты, выбранные для расчёта. Каждый фильм имеет одинаковый вес внутри совместимой шкалы.\n\n")
	if offset < 0 || offset >= len(summary.Evidence) {
		offset = 0
	}
	end := min(offset+3, len(summary.Evidence))
	for _, e := range summary.Evidence[offset:end] {
		fmt.Fprintf(&b, "🎞 %s · %s\n%s\nПросмотр #%d, версия %d · опрос #%d\n%d содержательных ответов\n", clipProfile(e.Film, 100), watchDate(e.Date), clubprofile.ResearchFamilyLabel(e.Family), e.WatchID, e.WatchRevision, e.PollID, e.Answers)
		for _, o := range e.Options {
			fmt.Fprintf(&b, "• %s: %d/%d\n", clipProfile(o.Text, 100), o.Count, e.Answers)
		}
		b.WriteString("\n")
		k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{profileButton(fmt.Sprintf("Опрос #%d", e.PollID), "poll", e.PollID)})
	}
	if len(summary.Evidence) == 0 {
		b.WriteString("Достаточных совместимых наблюдений пока нет.\n")
	}
	fmt.Fprintf(&b, "Не вошли: %d наблюдений с менее чем 3 ответами, %d несовместимых. Повторы и старые просмотры одного фильма не увеличивают вес.\nПроценты сводки — средние доли ответов по фильмам, не процент участников.", summary.Provisional, summary.Quarantined)
	if end < len(summary.Evidence) {
		k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{profileButton("Дальше →", "evidence", int64(end))})
	}
	if offset > 0 {
		k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{profileButton("← Назад", "evidence", int64(max(0, offset-3)))})
	}
	k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{profileButton("← Особенности", "research", 0)})
	return limitMenuText(b.String()), k
}

func researchGroupHeading(f clubprofile.ResearchFamilySummary) string {
	switch {
	case f.Divided():
		return "⚖️ Мнения разделились\n"
	case f.Films >= 3:
		return "📌 Что известно\n"
	default:
		return "🌱 Пока мало данных\n"
	}
}
