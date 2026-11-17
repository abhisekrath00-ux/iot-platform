package admissionoutbox

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// ErrOwnership is fail-closed: another batch may be active, or the dedicated
// guard cannot establish compatible local-file ownership. It grants no retry.
var ErrOwnership = errors.New("staged publisher ownership unavailable; no exchange started")

const ownerSQL = "CREATE TABLE admission_publisher_owner(version INTEGER NOT NULL CHECK(version=1))"

// canonicalStorePath converges relative spelling and existing symlink aliases.
// The database and its sidecar MUST stay in a trusted local directory. Do not
// hardlink, rename, copy, replace or remove either while a publisher is running.
// This is coordination between cooperating local processes, not a defense
// against a process that can tamper with the directory or a network filesystem.
func canonicalStorePath(path string) (string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	p := filepath.Join(parent, filepath.Base(path))
	actual, err := filepath.EvalSymlinks(p)
	if err == nil {
		return actual, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	return "", err
}

type publisherOwnership struct {
	db   *sql.DB
	conn *sql.Conn
}

// acquireOwnership holds one SQLite BEGIN IMMEDIATE transaction on a separate
// adjacent database for the WHOLE explicit batch, including any blocked Exchange
// and reconciliation. busy_timeout=0 means refuse instead of queueing a future
// send. No PID file, wall-clock expiry, heartbeat takeover or automatic resend.
// OS handle/process death releases the lock; retained input is never cleared by
// ownership. This deliberately does not hold the input DB's write transaction.
func (s *Store) acquireOwnership(ctx context.Context) (*publisherOwnership, error) {
	if s == nil || s.path == "" || ctx == nil {
		return nil, ErrOwnership
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := s.path + ".publisher-owner.sqlite"
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() {
			return nil, ErrOwnership
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, ErrOwnership
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(0)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, ErrOwnership
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, ErrOwnership
	}
	o := &publisherOwnership{db: db, conn: conn}
	fail := func() (*publisherOwnership, error) { o.release(false); return nil, ErrOwnership }
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fail()
	}
	rows, err := conn.QueryContext(ctx, `SELECT name,sql FROM sqlite_master WHERE name NOT GLOB 'sqlite_*'`)
	if err != nil {
		return fail()
	}
	names := 0
	compatible := true
	for rows.Next() {
		var name, definition string
		if rows.Scan(&name, &definition) != nil {
			compatible = false
			break
		}
		names++
		if name != "admission_publisher_owner" || definition != ownerSQL {
			compatible = false
		}
	}
	if rows.Err() != nil {
		compatible = false
	}
	rows.Close()
	if !compatible || names > 1 {
		return fail()
	}
	if names == 0 {
		if _, err = conn.ExecContext(ctx, ownerSQL+";INSERT INTO admission_publisher_owner VALUES(1)"); err != nil {
			return fail()
		}
	} else {
		var version, count int
		if err = conn.QueryRowContext(ctx, `SELECT version,(SELECT count(*) FROM admission_publisher_owner) FROM admission_publisher_owner`).Scan(&version, &count); err != nil || version != 1 || count != 1 {
			return fail()
		}
	}
	if err = ctx.Err(); err != nil {
		o.release(false)
		return nil, err
	}
	return o, nil
}
func (o *publisherOwnership) release(commit bool) error {
	if o == nil {
		return nil
	}
	// Release must run even after caller cancellation, bounded independently.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := "ROLLBACK"
	if commit {
		command = "COMMIT"
	}
	_, err := o.conn.ExecContext(ctx, command)
	closeErr := o.conn.Close()
	dbErr := o.db.Close()
	return errors.Join(err, closeErr, dbErr)
}
