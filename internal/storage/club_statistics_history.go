package storage

import (
	"context"
	"encoding/json"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (s *Store) StatisticsReports(ctx context.Context, chat int64) ([]clubprofile.StatisticsReport, error) {
	if chat == 0 {
		return nil, clubprofile.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT snap.id,d.state,d.reason,snap.body,snap.snapshot_json FROM club_statistics_snapshots snap JOIN club_statistics_deliveries d ON d.snapshot_id=snap.id WHERE snap.chat_id=? ORDER BY snap.id DESC LIMIT 20`, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clubprofile.StatisticsReport
	for rows.Next() {
		var r clubprofile.StatisticsReport
		var raw string
		if err = rows.Scan(&r.ID, &r.State, &r.Reason, &r.Text, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &r.Snapshot); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
