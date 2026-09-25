package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

func TestPreparationClaimRecoveryAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "retry.db")
	s, err := Open(ctx, path)
	must(t, err)
	setup(t, s, -1)
	scs, err := s.Schedules(ctx, -1)
	must(t, err)
	var sc daily.Schedule
	for _, v := range scs {
		if v.Kind == daily.Fact {
			sc = v
		}
	}
	now := time.Now().Truncate(time.Second)
	id, err := s.Reserve(ctx, sc, "2026-09-22", now.Unix(), now.Add(6*time.Hour).Unix())
	must(t, err)
	ok, err := s.ClaimPreparation(ctx, id, now)
	must(t, err)
	if !ok {
		t.Fatal("not claimed")
	}
	ok, err = s.ClaimPreparation(ctx, id, now)
	must(t, err)
	if ok {
		t.Fatal("claimed twice")
	}
	must(t, s.Close())
	s, err = Open(ctx, path)
	must(t, err)
	defer s.Close()
	must(t, s.Recover(ctx))
	rows, err := s.Preparing(ctx)
	must(t, err)
	if len(rows) != 1 || rows[0].FetchAttempts != 1 || rows[0].NextAttempt < now.Add(5*time.Minute).Unix() {
		t.Fatal(rows)
	}
	ok, err = s.ClaimPreparation(ctx, id, now)
	must(t, err)
	if ok {
		t.Fatal("recovery caused immediate retry")
	}
	when := time.Unix(rows[0].NextAttempt, 0)
	ok, err = s.ClaimPreparation(ctx, id, when)
	must(t, err)
	if !ok {
		t.Fatal("retry never became due")
	}
	must(t, s.DeferPreparation(ctx, id, when.Add(15*time.Minute), "source_unavailable"))
	must(t, s.Recover(ctx))
	rows, err = s.Preparing(ctx)
	must(t, err)
	if len(rows) != 1 || rows[0].FetchAttempts != 2 || rows[0].NextAttempt != when.Add(15*time.Minute).Unix() {
		t.Fatal(rows)
	}
	// Pausing wins over a result/deferral from an in-flight request.
	must(t, s.SetSchedule(ctx, 777, -1, daily.Fact, "09:00", false, when))
	must(t, s.DeferPreparation(ctx, id, when.Add(time.Hour), "source_unavailable"))
	rows, err = s.Preparing(ctx)
	must(t, err)
	if len(rows) != 0 {
		t.Fatal("cancelled slot revived")
	}
}

func TestPreparationMigrationPreservesExistingReadyDelivery(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	p, err := s.migrator()
	must(t, err)
	_, err = p.DownTo(ctx, 5)
	must(t, err)
	setup(t, s, -1)
	item, err := s.Add(ctx, 1, fact(-1, "before-upgrade"), testNow)
	must(t, err)
	must(t, s.Moderate(ctx, 2, -1, item, 42, true, testNow))
	_, err = s.db.Exec(`INSERT INTO deliveries(chat_id,kind,local_date,item_id,deadline,state) VALUES(-1,'fact','2026-09-22',?,?,'ready')`, item, testNow.Add(2*time.Hour).Unix())
	must(t, err)
	_, err = s.db.Exec(`UPDATE items SET state='reserved' WHERE id=?`, item)
	must(t, err)
	_, err = p.Up(ctx)
	must(t, err)
	rows, err := s.Pending(ctx)
	must(t, err)
	if len(rows) != 1 || rows[0].Item.ID != item {
		t.Fatal(rows)
	}
	var slot int64
	must(t, s.db.QueryRow("SELECT slot_at FROM deliveries WHERE id=?", rows[0].ID).Scan(&slot))
	if slot != testNow.Add(time.Hour).Unix() {
		t.Fatal(slot)
	}
	ok, err := s.Claim(ctx, rows[0].ID, testNow.Add(time.Hour))
	must(t, err)
	if !ok {
		t.Fatal("upgrade prevented delivery of reserved item")
	}
}
