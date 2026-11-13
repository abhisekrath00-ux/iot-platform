package cmdexec

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// ReplayStore durably claims a validated request BEFORE any actuator is called.
// A failed/uncertain claim must never fall back to process-local memory.
type ReplayStore interface {
	Claim(string, time.Time, time.Time) error
}

// SQLiteReplay survives process restart, but not disk loss, deletion, rollback
// or restoring an older backup. It does not make physical effects transactional.
type SQLiteReplay struct{ db *sql.DB }

const replayLimit = 100_000

func OpenReplay(path string) (*SQLiteReplay, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrReplayStore
	}
	// Create with private Unix permissions. Windows ACL inheritance still applies.
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		err = f.Close()
	} else if errors.Is(err, os.ErrExist) {
		var st os.FileInfo
		st, err = os.Lstat(abs)
		if err == nil && (!st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0) {
			err = ErrReplayStore
		}
	}
	if err != nil {
		return nil, ErrReplayStore
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	db, err := sql.Open("sqlite", u.String()+"?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(1000)")
	if err != nil {
		return nil, ErrReplayStore
	}
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS command_replay(request_id TEXT PRIMARY KEY, expires_ns INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS command_replay_expiry ON command_replay(expires_ns);
 CREATE TABLE IF NOT EXISTS command_clock(id INTEGER PRIMARY KEY CHECK(id=1), high_ns INTEGER NOT NULL);
 INSERT OR IGNORE INTO command_clock VALUES(1,0);`)
	if err != nil {
		db.Close()
		return nil, ErrReplayStore
	}
	return &SQLiteReplay{db: db}, nil
}
func (s *SQLiteReplay) Close() error { return s.db.Close() }
func (s *SQLiteReplay) Claim(id string, expires, now time.Time) error {
	if id == "" || len(id) > 256 || !now.Before(expires) {
		return ErrReplayStore
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrReplayStore
	}
	defer tx.Rollback()
	// The first write serializes independent connections/processes. A backwards
	// clock cannot resurrect an expired-and-pruned claim: refuse until caught up.
	r, err := tx.ExecContext(ctx, `UPDATE command_clock SET high_ns=? WHERE id=1 AND high_ns<=?`, now.UnixNano(), now.UnixNano())
	if err != nil {
		return ErrReplayStore
	}
	n, err := r.RowsAffected()
	if err != nil || n != 1 {
		return ErrReplayStore
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM command_replay WHERE expires_ns<=?`, now.UnixNano()); err != nil {
		return ErrReplayStore
	}
	var exists, count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM command_replay WHERE request_id=?`, id).Scan(&exists); err != nil {
		return ErrReplayStore
	}
	if exists != 0 {
		return ErrReplay
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM command_replay`).Scan(&count); err != nil || count >= replayLimit {
		return ErrReplayStore
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO command_replay VALUES(?,?)`, id, expires.UnixNano()); err != nil {
		return ErrReplayStore
	}
	if err = tx.Commit(); err != nil {
		return ErrReplayStore
	}
	return nil
}
