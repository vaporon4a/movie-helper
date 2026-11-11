package clubprofile

import (
	"fmt"
	"strings"
)

func statisticsClip(s string, limit int) string {
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit-1]) + "…"
	}
	return s
}
func overallStatisticsText(p Poll, w WatchEvent) string {
	n := (Observation{Question: p.Question, Result: *p.Result}).Answers()
	positive, negative, neutral := p.Result.Counts[3]+p.Result.Counts[4], p.Result.Counts[0]+p.Result.Counts[1], p.Result.Counts[2]
	heading := "Общее впечатление"
	if n >= 5 && positive >= 2 && negative >= 2 && positive*10 >= n*3 && negative*10 >= n*3 {
		heading = "Мнения разделились"
	}
	return fmt.Sprintf("🎞 %s · %s\nПонравился — %d · нейтрально — %d · не понравился — %d. Содержательных оценок: %d.", statisticsClip(w.Film.Label(), 90), heading, positive, neutral, negative, n)
}
func statisticsDistribution(p Poll) string {
	var parts []string
	for i, o := range p.Question.Options {
		if o.Substantive && p.Result.Counts[i] > 0 {
			parts = append(parts, fmt.Sprintf("%s — %d", statisticsClip(o.Text, 65), p.Result.Counts[i]))
		}
	}
	return strings.Join(parts, "; ") + "."
}
func choiceStatisticsText(p Poll) string {
	top := 0
	var leaders []string
	for i, o := range p.Question.Options {
		if !o.Substantive {
			continue
		}
		n := p.Result.Counts[i]
		if n > top {
			top = n
			leaders = nil
		}
		if n == top && top > 0 {
			leaders = append(leaders, statisticsClip(o.Text, 70))
		}
	}
	label := "🎭 Выбор жанра"
	if p.Question.Topic == Reference {
		label = "🎬 Выбор фильма"
	}
	n := (Observation{Question: p.Question, Result: *p.Result}).Answers()
	if len(leaders) > 1 {
		return fmt.Sprintf("%s · ничья\n%s — по %d из %d содержательных голосов. Среди предложенных вариантов.", label, statisticsLeaderNames(leaders), top, n)
	}
	return fmt.Sprintf("%s\nЛидер среди предложенных вариантов: %s — %d из %d содержательных голосов.", label, strings.Join(leaders, ", "), top, n)
}
func statisticsLeaderNames(leaders []string) string {
	text := strings.Join(leaders[:min(3, len(leaders))], ", ")
	if len(leaders) > 3 {
		text += fmt.Sprintf(" и ещё %d", len(leaders)-3)
	}
	return text
}

// SelectStatisticsClaims gives different films room before taking another aspect.
func SelectStatisticsClaims(claims []StatisticsClaim, limit int) []StatisticsClaim {
	out := make([]StatisticsClaim, 0, min(limit, len(claims)))
	seen := map[int64]bool{}
	used := map[string]bool{}
	for _, c := range claims {
		if len(out) >= limit {
			break
		}
		if !seen[c.FilmID] {
			out = append(out, c)
			seen[c.FilmID] = true
			used[c.ID] = true
		}
	}
	for _, c := range claims {
		if len(out) >= limit {
			break
		}
		if !used[c.ID] {
			out = append(out, c)
			used[c.ID] = true
		}
	}
	return out
}
func RenderStatisticsDigest(s StatisticsSnapshot) string {
	if len(s.Claims) == 0 {
		return "Новых содержательных итогов за этот период пока нет. Открытые опросы дождутся завершения."
	}
	var b strings.Builder
	b.WriteString("🍿 Что выяснил наш киноклуб\n")
	if !s.Period.Start.IsZero() {
		fmt.Fprintf(&b, "%s — %s · %s\n", s.Period.Start.Format("02.01.2006"), s.Period.End.AddDate(0, 0, -1).Format("02.01.2006"), s.Zone)
	}
	for _, c := range s.Claims {
		b.WriteString("\n" + c.Text + "\n")
		if !s.Period.Contains(c.ClosedAt) {
			fmt.Fprintf(&b, "Дополнение к прошлым итогам: опрос от %s.\n", c.ClosedAt.In(s.Period.Start.Location()).Format("02.01"))
		}
	}
	b.WriteString("\nЭто ответы в отдельных анонимных опросах, не число участников клуба. Подробнее — /movie_profile")
	return b.String()
}
