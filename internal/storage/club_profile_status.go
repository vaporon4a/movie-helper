package storage

import (
	"context"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (s *Store) ProfileStatus(ctx context.Context, chat int64) (clubprofile.ProfileStatus, error) {
	var status clubprofile.ProfileStatus
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE((SELECT auto_review FROM club_profile_settings WHERE chat_id=?),1)", chat).Scan(&status.AutoReview); err != nil {
		return status, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT poll_id,state,reason FROM club_poll_cleanup WHERE chat_id=? AND state IN ('blocked','expired') ORDER BY poll_id DESC LIMIT 3", chat)
	if err != nil {
		return status, err
	}
	defer rows.Close()
	for rows.Next() {
		var j clubprofile.CleanupJob
		j.ChatID = chat
		if err = rows.Scan(&j.PollID, &j.State, &j.Reason); err != nil {
			return status, err
		}
		status.CleanupIssues = append(status.CleanupIssues, j)
	}
	return status, rows.Err()
}
