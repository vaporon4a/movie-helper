package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (s *Store) ReserveStatistics(ctx context.Context, sc clubprofile.StatisticsSettings, snapshot clubprofile.StatisticsSnapshot, slot time.Time) error {
	if sc.ChatID == 0 || snapshot.ChatID != sc.ChatID || !sc.Enabled || snapshot.Zone != sc.Zone {
		return clubprofile.ErrInvalid
	}
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		valid, err := statisticsReservationAllowed(ctx, tx, sc, slot)
		if err != nil || !valid {
			return err
		}
		snapshot.Claims, err = unusedStatisticsClaims(ctx, tx, sc.ChatID, snapshot.Claims)
		if err != nil {
			return err
		}
		snapshot.Claims = clubprofile.SelectStatisticsClaims(snapshot.Claims, 3)
		id, err := saveStatisticsSnapshot(ctx, tx, sc, snapshot, slot)
		if err != nil {
			return err
		}
		state := "pending"
		if len(snapshot.Claims) == 0 {
			state = "skipped"
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO club_statistics_deliveries(chat_id,snapshot_id,settings_revision,state,deadline,next_attempt) VALUES(?,?,?,?,?,?)`, sc.ChatID, id, sc.Revision, state, slot.Add(24*time.Hour).Unix(), slot.Unix())
		if err != nil {
			return err
		}
		return saveStatisticsClaims(ctx, tx, sc.ChatID, id, snapshot.Claims)
	})
}
func statisticsReservationAllowed(ctx context.Context, tx *sql.Tx, sc clubprofile.StatisticsSettings, slot time.Time) (bool, error) {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM club_statistics_settings s JOIN chats c USING(chat_id) WHERE s.chat_id=? AND s.revision=? AND s.enabled=1 AND c.active=1 AND c.zone=? AND s.effective<?)
 AND NOT EXISTS(SELECT 1 FROM club_statistics_snapshots WHERE chat_id=? AND week_key=?)
 AND NOT EXISTS(SELECT 1 FROM club_statistics_snapshots snap JOIN club_statistics_deliveries d ON d.snapshot_id=snap.id WHERE snap.chat_id=? AND snap.zone<>? AND snap.period_start<? AND snap.period_end>? AND d.state IN ('pending','retry','sending','sent','unknown'))`, sc.ChatID, sc.Revision, sc.Zone, slot.Unix(), sc.ChatID, clubprofile.DigestWeekKey(slot), sc.ChatID, sc.Zone, clubprofile.DigestPeriod(slot).End.Unix(), clubprofile.DigestPeriod(slot).Start.Unix()).Scan(&valid)
	return valid, err
}
func unusedStatisticsClaims(ctx context.Context, tx *sql.Tx, chat int64, claims []clubprofile.StatisticsClaim) ([]clubprofile.StatisticsClaim, error) {
	out := make([]clubprofile.StatisticsClaim, 0, len(claims))
	for _, c := range claims {
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM club_statistics_claims WHERE chat_id=? AND claim_id=?)", chat, c.ID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			out = append(out, c)
		}
	}
	return out, nil
}
func (s *Store) StatisticsDue(ctx context.Context, now time.Time) ([]clubprofile.StatisticsDelivery, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE club_statistics_deliveries SET state='expired',reason='deadline' WHERE state IN ('pending','retry') AND deadline<=?`, now.Unix())
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,d.chat_id,s.body FROM club_statistics_deliveries d JOIN club_statistics_snapshots s ON s.id=d.snapshot_id WHERE d.state IN ('pending','retry') AND d.next_attempt<=? AND d.deadline>? ORDER BY d.id`, now.Unix(), now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clubprofile.StatisticsDelivery
	for rows.Next() {
		var d clubprofile.StatisticsDelivery
		if err = rows.Scan(&d.ID, &d.ChatID, &d.Text); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) ClaimStatistics(ctx context.Context, id int64, now time.Time) (bool, error) {
	r, err := s.db.ExecContext(ctx, `UPDATE club_statistics_deliveries SET state='sending' WHERE id=? AND state IN ('pending','retry') AND next_attempt<=? AND deadline>? AND EXISTS(SELECT 1 FROM chats c JOIN club_statistics_settings s USING(chat_id) JOIN club_statistics_snapshots snap ON snap.id=club_statistics_deliveries.snapshot_id WHERE c.chat_id=club_statistics_deliveries.chat_id AND c.active=1 AND s.enabled=1 AND s.revision=club_statistics_deliveries.settings_revision AND c.zone=snap.zone)`, id, now.Unix(), now.Unix())
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}
func (s *Store) FinishStatistics(ctx context.Context, id int64, state string, message int, next time.Time, reason string) error {
	switch state {
	case "sent", "retry", string(clubprofile.Failed), "unknown":
	default:
		return clubprofile.ErrInvalid
	}
	r, err := s.db.ExecContext(ctx, `UPDATE club_statistics_deliveries SET state=?,message_id=?,next_attempt=?,reason=? WHERE id=? AND state='sending'`, state, message, next.Unix(), reason, id)
	return profileChanged(r, err)
}
func (s *Store) RecoverStatistics(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE club_statistics_deliveries SET state='unknown',reason='restart_during_send' WHERE state='sending'`)
	return err
}

func saveStatisticsSnapshot(ctx context.Context, tx *sql.Tx, sc clubprofile.StatisticsSettings, snapshot clubprofile.StatisticsSnapshot, slot time.Time) (int64, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return 0, err
	}
	r, err := tx.ExecContext(ctx, `INSERT INTO club_statistics_snapshots(chat_id,week_key,zone,period_start,period_end,as_of,version,snapshot_json,body) VALUES(?,?,?,?,?,?,?,?,?)`, sc.ChatID, clubprofile.DigestWeekKey(slot), sc.Zone, snapshot.Period.Start.Unix(), snapshot.Period.End.Unix(), snapshot.AsOf.Unix(), snapshot.Version, raw, clubprofile.RenderStatisticsDigest(snapshot))
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func saveStatisticsClaims(ctx context.Context, tx *sql.Tx, chat, id int64, claims []clubprofile.StatisticsClaim) error {
	for _, c := range claims {
		if _, err := tx.ExecContext(ctx, `INSERT INTO club_statistics_claims(chat_id,claim_id,snapshot_id) VALUES(?,?,?)`, chat, c.ID, id); err != nil {
			return err
		}
	}
	return nil
}
