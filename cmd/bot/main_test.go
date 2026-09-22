package main

import (
	"path/filepath"
	"testing"
)

func TestExclusiveDBLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "db.sqlite")
	a, err := lockDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := lockDB(path); err == nil {
		b.Close()
		t.Fatal("second process lock accepted")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := lockDB(path)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
}
