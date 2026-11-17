package admissionoutbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"time"
)

const journalMetaSQL = "CREATE TABLE collection_meta(version INTEGER NOT NULL,scope_sha256 TEXT NOT NULL)"
const journalPendingSQL = "CREATE TABLE collection_pending(id INTEGER PRIMARY KEY CHECK(id=1),payload BLOB NOT NULL,digest TEXT NOT NULL)"
const maxJournalPayload = 256*8192 + 1024

// JournalConfig selects a NEW separate file, never the retained or legacy queue.
// No migration is attempted. Trusted LOCAL directory/intact ownership sidecar
// required; no hardlink/copy/rename/networkfs/hostile-directory guarantees.
type JournalConfig struct {
	Enabled bool
	Path    string
}
type collectionJournal struct {
	db    *sql.DB
	owner *publisherOwnership
	scope string
}

func digestBytes(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func (c *Collector) journalScope() string {
	b, _ := json.Marshal(struct {
		Tenant, Gateway, Device, RetainedPath string
		Points                                map[string]string
	}{c.session.tenant, c.session.gateway, c.device, c.session.store.path, c.points})
	return digestBytes(b)
}
func (c *Collector) journalBytes(input []Reading) ([]byte, error) {
	if len(input) != len(c.points) || len(input) < 1 || len(input) > 256 {
		return nil, ErrEvidence
	}
	seen := map[string]bool{}
	ids := map[[2]string]bool{}
	var observed time.Time
	for _, r := range input {
		if _, _, e := canonical(r); e != nil {
			return nil, ErrEvidence
		}
		unit, ok := c.points[r.PointID]
		key := [2]string{r.EventID, r.ObservedAt.Format(time.RFC3339Nano)}
		if !ok || seen[r.PointID] || ids[key] || r.TenantID != c.session.tenant || r.GatewayID != c.session.gateway || r.DeviceID != c.device || r.Unit != unit || r.Quality != "measured" {
			return nil, ErrEvidence
		}
		if observed.IsZero() {
			observed = r.ObservedAt
		} else if observed != r.ObservedAt {
			return nil, ErrEvidence
		}
		seen[r.PointID] = true
		ids[key] = true
	}
	b, e := json.Marshal(input)
	if e != nil || len(b) > maxJournalPayload {
		return nil, ErrEvidence
	}
	return b, nil
}
func openCollectionJournal(ctx context.Context, c *Collector, cfg JournalConfig) (j *collectionJournal, pending []Reading, err error) {
	if !cfg.Enabled {
		return nil, nil, ErrDisabled
	}
	if ctx == nil || cfg.Path == "" || !filepath.IsAbs(cfg.Path) {
		return nil, nil, ErrInput
	}
	path, e := canonicalStorePath(cfg.Path)
	if e != nil {
		return nil, nil, e
	}
	// Do not even open the retained DB or its dedicated guard as a journal.
	if path == c.session.store.path || path == c.session.store.path+".publisher-owner.sqlite" {
		return nil, nil, ErrInput
	}
	owner, e := (&Store{path: path + ".collection"}).acquireOwnership(ctx)
	if e != nil {
		return nil, nil, e
	}
	j = &collectionJournal{owner: owner, scope: c.journalScope()}
	defer func(opened *collectionJournal) {
		if err != nil {
			opened.close()
			j = nil
		}
	}(j)
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	j.db, err = sql.Open("sqlite", u.String())
	if err != nil {
		return nil, nil, err
	}
	j.db.SetMaxOpenConns(1)
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return j, nil, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT name,sql FROM sqlite_master WHERE name NOT GLOB 'sqlite_*'`)
	if e != nil {
		return j, nil, e
	}
	names := map[string]string{}
	for rows.Next() {
		var n, s string
		if e = rows.Scan(&n, &s); e != nil {
			break
		}
		names[n] = s
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return j, nil, e
	}
	if len(names) == 0 {
		if _, e = tx.ExecContext(ctx, journalMetaSQL+";"+journalPendingSQL); e == nil {
			_, e = tx.ExecContext(ctx, "INSERT INTO collection_meta VALUES(1,?)", j.scope)
		}
	} else {
		if len(names) != 2 || names["collection_meta"] != journalMetaSQL || names["collection_pending"] != journalPendingSQL {
			return j, nil, ErrSchema
		}
		var version, count int
		var scope string
		e = tx.QueryRowContext(ctx, `SELECT version,scope_sha256,(SELECT count(*) FROM collection_meta) FROM collection_meta`).Scan(&version, &scope, &count)
		if e == nil && (version != 1 || count != 1 || scope != j.scope) {
			e = ErrSchema
		}
	}
	if e != nil {
		return j, nil, e
	}
	if e = tx.Commit(); e != nil {
		return j, nil, e
	}
	var mode string
	if e = j.db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); e != nil {
		return j, nil, e
	}
	if mode != "wal" {
		return j, nil, ErrSchema
	}
	if _, e = j.db.ExecContext(ctx, "PRAGMA synchronous=FULL"); e != nil {
		return j, nil, e
	}
	var length int
	e = j.db.QueryRowContext(ctx, "SELECT length(payload) FROM collection_pending WHERE id=1").Scan(&length)
	if errors.Is(e, sql.ErrNoRows) {
		return j, nil, nil
	}
	if e != nil {
		return j, nil, e
	}
	if length < 1 || length > maxJournalPayload {
		return j, nil, ErrEvidence
	}
	var b []byte
	var digest string
	if e = j.db.QueryRowContext(ctx, "SELECT payload,digest FROM collection_pending WHERE id=1").Scan(&b, &digest); e != nil {
		return j, nil, e
	}
	if digest != digestBytes(b) || json.Unmarshal(b, &pending) != nil {
		return j, nil, ErrEvidence
	}
	canonical, e := c.journalBytes(pending)
	if e != nil || !bytes.Equal(canonical, b) {
		return j, nil, ErrEvidence
	}
	return j, pending, nil
}

// save refuses replacement. A caller with uncertain Save must retry identical
// bytes; there is one pending sample, no eviction, replay worker or retry loop.
func (j *collectionJournal) save(ctx context.Context, b []byte) error {
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var saved []byte
	var digest string
	e = tx.QueryRowContext(ctx, "SELECT payload,digest FROM collection_pending WHERE id=1").Scan(&saved, &digest)
	if e == nil {
		if digest != digestBytes(saved) {
			return ErrEvidence
		}
		if !bytes.Equal(saved, b) {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO collection_pending VALUES(1,?,?)", b, digestBytes(b)); e != nil {
		return e
	}
	return tx.Commit()
}
func (j *collectionJournal) clear(ctx context.Context, b []byte) error {
	tx, e := j.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var saved []byte
	var digest string
	e = tx.QueryRowContext(ctx, "SELECT payload,digest FROM collection_pending WHERE id=1").Scan(&saved, &digest)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if digest != digestBytes(saved) {
		return ErrEvidence
	}
	if !bytes.Equal(saved, b) {
		return ErrConflict
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM collection_pending WHERE id=1"); e != nil {
		return e
	}
	return tx.Commit()
}
func (j *collectionJournal) close() error {
	if j == nil {
		return nil
	}
	var e error
	if j.db != nil {
		e = j.db.Close()
	}
	return errors.Join(e, j.owner.release(false))
}

// NewJournalCollector opens a separate opt-in journal only after ordinary
// collector validation. Reopen loads/revalidates exact scope and pending sample;
// it NEVER polls, retains or sends. Recovery requires explicit RetainPending.
func NewJournalCollector(ctx context.Context, cfg CollectorConfig, jcfg JournalConfig) (*Collector, error) {
	if !jcfg.Enabled {
		return nil, ErrDisabled
	}
	c, e := NewCollector(cfg)
	if e != nil {
		return nil, e
	}
	j, pending, e := openCollectionJournal(ctx, c, jcfg)
	if e != nil {
		return nil, e
	}
	c.journal = j
	c.pending = pending
	if len(pending) > 0 {
		c.last = CollectionResult{Sampled: len(pending), Pending: len(pending)}
		c.journalConfirmed = true
		c.outcome = "journal-recovered"
	}
	return c, nil
}

// Close prevents ALL collector calls, drains them, then closes the journal and
// releases local ownership. Pending is NOT discarded. Stop alone keeps journal
// ownership and still permits explicit retention. Caller owns source/Session.
func (c *Collector) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		return ErrInput
	}
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	if e := c.Stop(ctx); e != nil {
		return e
	}
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil
	}
	e := c.journal.close()
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return e
}
