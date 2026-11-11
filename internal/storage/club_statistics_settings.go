package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

const statisticsSettingsColumns = `c.chat_id,c.zone,COALESCE(s.clock,'09:00'),COALESCE(s.weekday,5),COALESCE(s.enabled,0),COALESCE(s.revision,0),COALESCE(s.effective,0)`

func scanStatisticsSettings(row interface{ Scan(...any) error }) (clubprofile.StatisticsSettings, error) {
	var s clubprofile.StatisticsSettings
	var effective int64
	err := row.Scan(&s.ChatID, &s.Zone, &s.Clock, &s.Weekday, &s.Enabled, &s.Revision, &effective)
	s.Effective = time.Unix(effective, 0)
	return s, err
}
func (s *Store) StatisticsSettings(ctx context.Context, chat int64) (clubprofile.StatisticsSettings, error) {
	if chat == 0 {
		return clubprofile.StatisticsSettings{}, clubprofile.ErrInvalid
	}
	return scanStatisticsSettings(s.db.QueryRowContext(ctx, "SELECT "+statisticsSettingsColumns+" FROM chats c LEFT JOIN club_statistics_settings s ON s.chat_id=c.chat_id WHERE c.chat_id=?", chat))
}
func (s *Store) StatisticsSchedules(ctx context.Context) ([]clubprofile.StatisticsSettings, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+statisticsSettingsColumns+" FROM chats c JOIN club_statistics_settings s ON s.chat_id=c.chat_id WHERE c.active=1 AND s.enabled=1 ORDER BY c.chat_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clubprofile.StatisticsSettings
	for rows.Next() {
		sc, e := scanStatisticsSettings(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}
func (s *Store) SetStatisticsSettings(ctx context.Context, sc clubprofile.StatisticsSettings, now time.Time) error {
	if sc.ChatID == 0 || sc.Zone == "" {
		return clubprofile.ErrInvalid
	}
	if _, err := clubprofile.WeeklySlot(now, sc.Weekday, sc.Clock, sc.Zone); err != nil {
		return err
	}
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO club_statistics_settings(chat_id) VALUES(?)", sc.ChatID)
		if err != nil {
			return err
		}
		r, err := tx.ExecContext(ctx, `UPDATE club_statistics_settings SET weekday=?,clock=?,enabled=?,revision=revision+1,effective=? WHERE chat_id=? AND revision=? AND EXISTS(SELECT 1 FROM chats WHERE chat_id=? AND zone=?)`, sc.Weekday, sc.Clock, sc.Enabled, now.Unix(), sc.ChatID, sc.Revision, sc.ChatID, sc.Zone)
		if err = profileChanged(r, err); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE club_statistics_deliveries SET state='cancelled',reason='settings_changed' WHERE chat_id=? AND state IN ('pending','retry')`, sc.ChatID)
		return err
	})
}
