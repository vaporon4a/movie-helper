package clubprofile

import (
	"context"
	"slices"
	"time"
)

// PreparationState is a read projection; provider payloads never cross this port.
type PreparationState struct {
	WatchID, SessionID                   int64
	WatchRevision, PreparedRevision      int
	EnrollmentMode, EffectiveMode, Stage string
	UpdatedAt                            time.Time
	Approved                             bool
}
type ProfilePanelRepository interface {
	ProfilePageRevision(context.Context, int64, int) (int, error)
	ProfilePreparationStates(context.Context, int64) ([]PreparationState, error)
	SaveProfilePage(context.Context, int64, int, string, int64) error
	ProfilePage(context.Context, int64, int) (string, int64, bool, error)
}

func (p PreparationState) Label(s ReviewSession) string {
	if s.Plan.Version == CoverageV2 {
		return "Адресный план принят; собираем ответы по особенностям фильма"
	}
	if p.Stage == "fallback" {
		return "Идёт обычная серия вопросов; адресный набор её не заменит"
	}
	if p.WatchRevision != p.PreparedRevision {
		return "Подготовка требует актуальных данных просмотра"
	}
	if p.EffectiveMode == "off" {
		return "Адресная подготовка выключена; доступны обычные вопросы"
	}
	switch p.Stage {
	case "ready":
		if p.Approved && p.EnrollmentMode == "on" && p.EffectiveMode == "on" {
			return "Вопросы подготовлены, ждём начала обсуждения"
		}
		return "Подготовка в пробном режиме; публикация обычных вопросов"
	case "sources":
		return "Собираем сведения о фильме для адресных вопросов"
	case "generate":
		return "Готовим вопросы по особенностям фильма"
	case "audit":
		return "Проверяем вопросы и основания для анализа"
	case "rejected":
		return "Адресный набор не прошёл проверку; доступны обычные вопросы"
	case "superseded":
		return "Подготовка остановлена; план пока не принят"
	case "frozen":
		return "План вопросов принят"
	default:
		return "Ожидает подготовки вопросов"
	}
}

// ReviewQueue is the ordering used by the scheduler and visible queue alike.
func ReviewQueue(sessions []ReviewSession) []ReviewSession {
	out := make([]ReviewSession, 0, len(sessions))
	for _, s := range sessions {
		if s.State == "queued" || s.State == "researching" || s.State == "blocked" {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b ReviewSession) int {
		aq, bq := a.State == "queued", b.State == "queued"
		if aq != bq {
			if aq {
				return 1
			}
			return -1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out
}
