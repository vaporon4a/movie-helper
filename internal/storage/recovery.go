package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/featurevote"
	"github.com/vaporon4a/movie-helper/internal/interaction"
	"github.com/vaporon4a/movie-helper/internal/recovery"
)

func recoveryIssues(ctx context.Context, tx *sql.Tx, chat int64, now time.Time) ([]recovery.Issue, error) {
	var out []recovery.Issue
	queries := []struct{ domain, table string }{{dailyScope, "deliveries"}, {"club", "club_reminder_deliveries"}, {"movies", "club_profile_polls"}, {"ideas", "feature_rounds"}}
	for _, q := range queries {
		issues, err := readRecoveryRows(ctx, tx, chat, q.domain, q.table)
		if err != nil {
			return nil, err
		}
		for _, i := range issues {
			if err = decorateRecoveryIssue(ctx, tx, chat, &i, now); err != nil {
				return nil, err
			}
			out = append(out, i)
		}
	}
	panels, err := surfaceIssues(ctx, tx, chat, now)
	if err != nil {
		return nil, err
	}
	return append(out, panels...), nil
}
func (s *Store) RecoveryIssues(ctx context.Context, chat int64, now time.Time) ([]recovery.Issue, error) {
	var out []recovery.Issue
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error { var e error; out, e = recoveryIssues(ctx, tx, chat, now); return e })
	return out, err
}
func decorateRecoveryIssue(ctx context.Context, tx *sql.Tx, chat int64, i *recovery.Issue, now time.Time) error {
	i.Label = recoveryLabel(*i)
	sent := recovery.Action{Key: "sent", Label: "Сообщение есть", Consequence: "Подтвердить уже состоявшуюся отправку. Нового сообщения не будет."}
	cancel := recovery.Action{Key: "cancel", Label: "Отменить…", Consequence: "Операция будет отменена; собранная история сохраняется."}
	retry := recovery.Action{Key: retryState, Label: "Проверил, повторить…", Consequence: "Вы проверили чат и не нашли сообщение. Повтор может создать дубль, если прошлое сообщение всё же пришло."}
	switch i.Domain {
	case dailyScope:
		if i.State == unknownState {
			i.Actions = []recovery.Action{sent, {Key: "requeue", Label: "Вернуть материал на следующий день…", Consequence: "Материал вернётся в очередь на следующий доступный день. Немедленного сообщения не будет; сначала проверьте чат на дубль."}}
		}
	case "club":
		d, err := scanClubDelivery(tx.QueryRowContext(ctx, "SELECT "+clubDeliveryColumns+" FROM club_reminder_deliveries WHERE id=? AND chat_id=?", i.ID, chat))
		if err != nil {
			return err
		}
		if d.State == unknownState {
			i.Actions = []recovery.Action{sent, cancel}
			if _, e := clubResolveTarget(retryState, d, now); e == nil {
				i.Actions = append(i.Actions, retry)
			}
		}
	case "movies":
		p, err := scanProfilePoll(tx.QueryRowContext(ctx, "SELECT "+profilePollColumns+" FROM club_profile_polls WHERE id=? AND chat_id=?", i.ID, chat))
		if err != nil {
			return err
		}
		cancel.Consequence = "Вопрос будет отменён. Для серии о просмотре это может означать неполное исследование; ответы и просмотр сохранятся. Это не закрытие меню."
		i.Actions = []recovery.Action{cancel}
		if _, e := profileResolveTarget(p, retryState, now); e == nil {
			i.Actions = append(i.Actions, retry)
		}
	case "ideas":
		i.Actions = []recovery.Action{sent, retry, cancel}
	}
	return nil
}
func (s *Store) PrepareRecovery(ctx context.Context, c recovery.Confirmation, now time.Time) (recovery.Confirmation, error) {
	var err error
	c.Token, err = interaction.Token()
	if err != nil {
		return c, err
	}
	c.Expires = now.Add(10 * time.Minute)
	err = s.transaction(ctx, nil, func(tx *sql.Tx) error {
		issues, e := recoveryIssues(ctx, tx, c.ChatID, now)
		if e != nil {
			return e
		}
		n := slices.IndexFunc(issues, func(i recovery.Issue) bool { return i.Domain == c.Issue.Domain && i.ID == c.Issue.ID })
		if n < 0 {
			return recovery.ErrConflict
		}
		c.Issue = issues[n]
		if !slices.ContainsFunc(c.Issue.Actions, func(a recovery.Action) bool { return a.Key == c.Action }) {
			return recovery.ErrConflict
		}
		raw, e := json.Marshal(c)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO recovery_confirmations(token,chat_id,panel_id,body,expires_at) VALUES(?,?,?,?,?)", c.Token, c.ChatID, c.PanelID, raw, c.Expires.Unix())
		return e
	})
	return c, err
}
func (s *Store) ApplyRecovery(ctx context.Context, token string, chat int64, panel int, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var raw string
		err := tx.QueryRowContext(ctx, "SELECT body FROM recovery_confirmations WHERE token=? AND chat_id=? AND panel_id=? AND expires_at>? AND NOT EXISTS(SELECT 1 FROM interaction_panels WHERE chat_id=? AND panel_id=? AND closed=1)", token, chat, panel, now.Unix(), chat, panel).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return recovery.ErrConflict
		}
		if err != nil {
			return err
		}
		var c recovery.Confirmation
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return err
		}
		issues, err := recoveryIssues(ctx, tx, chat, now)
		if err != nil {
			return err
		}
		n := slices.IndexFunc(issues, func(i recovery.Issue) bool {
			return i.Domain == c.Issue.Domain && i.ID == c.Issue.ID && i.Stamp == c.Issue.Stamp
		})
		if n < 0 || !slices.ContainsFunc(issues[n].Actions, func(a recovery.Action) bool { return a.Key == c.Action }) {
			return recovery.ErrConflict
		}
		if err = applyRecoveryAction(ctx, tx, chat, c, now); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM recovery_confirmations WHERE token=?", token)
		return err
	})
}
func applyRecoveryAction(ctx context.Context, tx *sql.Tx, chat int64, c recovery.Confirmation, now time.Time) error {
	switch c.Issue.Domain {
	case "ui":
		r, err := tx.ExecContext(ctx, "UPDATE interaction_surfaces SET state='rejected' WHERE rowid=? AND chat_id=? AND token=? AND state IN ('unknown','sending')", c.Issue.ID, chat, c.Issue.Stamp)
		return profileChanged(r, err)
	case dailyScope:
		return resolveDaily(ctx, tx, chat, c.Issue.ID, c.Action == "sent")
	case "club":
		return resolveClub(ctx, tx, chat, c.Issue.ID, c.Action, now)
	case "movies":
		return resolveProfilePoll(ctx, tx, chat, c.Issue.ID, c.Action, now)
	case "ideas":
		return resolveFeatureRound(ctx, tx, chat, c.Issue.ID, featurevote.ResolveAction(c.Action), now)
	default:
		return clubprofile.ErrInvalid
	}
}

func readRecoveryRows(ctx context.Context, tx *sql.Tx, chat int64, domain, table string) ([]recovery.Issue, error) { // #nosec G202 -- internal fixed identifiers, participant values are bound.
	rows, err := tx.QueryContext(ctx, "SELECT id,state,COALESCE((SELECT version FROM recovery_versions v WHERE v.domain=? AND v.object_id=t.id),0) FROM "+table+" t WHERE chat_id=? AND state IN ('unknown','failed') ORDER BY id DESC LIMIT 10", domain, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var issues []recovery.Issue
	for rows.Next() {
		i := recovery.Issue{Domain: domain}
		var rev int64
		if err = rows.Scan(&i.ID, &i.State, &rev); err != nil {
			return nil, err
		}
		i.Stamp = fmt.Sprint(rev)
		issues = append(issues, i)
	}
	err = rows.Err()
	if err != nil {
		return nil, err
	}
	return issues, nil
}

func surfaceIssues(ctx context.Context, tx *sql.Tx, chat int64, now time.Time) ([]recovery.Issue, error) {
	rows, err := tx.QueryContext(ctx, "SELECT rowid,token,state,purpose FROM interaction_surfaces WHERE chat_id=? AND (state='unknown' OR (state='sending' AND expires_at<=?)) ORDER BY rowid", chat, now.Add(24*time.Hour-time.Minute).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recovery.Issue
	for rows.Next() {
		i := recovery.Issue{Domain: "ui"}
		var purpose string
		if err = rows.Scan(&i.ID, &i.Stamp, &i.State, &purpose); err != nil {
			return nil, err
		}
		i.Label = "Доставка меню не подтверждена"
		i.Actions = []recovery.Action{{Key: "dismiss", Label: "Я проверил чат, разблокировать меню…", Consequence: "Повторной отправки сейчас не будет. Следующая явная команда сможет открыть новое меню. Если прежнее меню появилось, закройте его кнопкой; новая команда может создать ещё одно."}}
		out = append(out, i)
	}
	return out, rows.Err()
}

func recoveryLabel(i recovery.Issue) string {
	domain := map[string]string{dailyScope: "Мем / факт", "club": "Напоминание", "movies": "Киноопрос", "ideas": "Голосование за идеи"}[i.Domain]
	state := "ошибка"
	if i.State == unknownState {
		state = "доставка не подтверждена"
	}
	return fmt.Sprintf("%s #%d · %s", domain, i.ID, state)
}
