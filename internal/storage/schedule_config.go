package storage

import (
	"context"
	"database/sql"
	"time"
)

func configHeader(ctx context.Context, tx *sql.Tx, chat int64, domain string) (int64, bool, string, error) {
	var revision int64
	var active bool
	var zone string
	err := tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT revision FROM club_config_revisions WHERE chat_id=c.chat_id AND domain=?),0),c.active,c.zone FROM chats c WHERE c.chat_id=?", domain, chat).Scan(&revision, &active, &zone)
	return revision, active, zone, err
}
func validConfigClock(clock string) bool {
	_, err := time.Parse("15:04", clock)
	return err == nil && len(clock) == 5
}
