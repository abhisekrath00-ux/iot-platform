// Package admissionoutbox is an unwired, opt-in processing-v1 durability
// foundation. No production caller opens it. It does NOT replace queue.Queue.
// No network receiver, publisher or retry loop exists. The separately guarded
// ReconcileSigned API is opt-in only; matching untrusted receipts stays pure.
package admissionoutbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

var (
	ErrDisabled = errors.New("staged admission outbox disabled")
	ErrInput    = errors.New("staged admission outbox requires exact stable input")
	ErrConflict = errors.New("staged admission outbox identity conflict")
	ErrCapacity = errors.New("staged admission outbox full; input NOT retained")
	ErrEvidence = errors.New("staged admission outbox evidence mismatch")
	ErrSchema   = errors.New("staged admission outbox requires its own compatible database")
)

const Policy = "processing-v1"
const MaxCapacity = 100000
const metaSQL = "CREATE TABLE admission_meta(version INTEGER NOT NULL,policy TEXT NOT NULL,capacity INTEGER NOT NULL)"
const inputsSQL = "CREATE TABLE admission_inputs(sequence INTEGER PRIMARY KEY AUTOINCREMENT,event_id TEXT NOT NULL,observed_at TEXT NOT NULL,topic TEXT NOT NULL,payload BLOB NOT NULL,digest TEXT NOT NULL,UNIQUE(event_id,observed_at))"

// Reading field order and defaults are the server version1 canonical vector.
// The caller must create event/time ONCE, before Put, and retain them unchanged.
// Missing clocks, submicrosecond times and noncanonical IDs are refused, not fixed.
type Reading struct {
	EventID       string    `json:"event_id"`
	TenantID      string    `json:"tenant_id"`
	GatewayID     string    `json:"gateway_id"`
	DeviceID      string    `json:"device_id"`
	PointID       string    `json:"point_id"`
	ObservedAt    time.Time `json:"observed_at"`
	Value         float64   `json:"value"`
	Unit          string    `json:"unit"`
	Quality       string    `json:"quality"`
	SchemaVersion int       `json:"schema_version"`
}

// Receipt is a typed comparison value, not an accepted network message.
type Receipt struct {
	Version     int       `json:"version"`
	Policy      string    `json:"policy"`
	TenantID    string    `json:"tenant_id"`
	GatewayID   string    `json:"gateway_id"`
	DeviceID    string    `json:"device_id"`
	PointID     string    `json:"point_id"`
	EventID     string    `json:"event_id"`
	ObservedAt  time.Time `json:"observed_at"`
	InputSHA256 string    `json:"input_sha256"`
	State       string    `json:"state"`
	New         bool      `json:"new"`
}
type Item struct {
	Sequence    int64
	Topic       string
	Reading     Reading
	Payload     []byte
	InputSHA256 string
}
type Config struct {
	Enabled  bool
	Path     string
	Capacity int
}
type Store struct {
	db       *sql.DB
	capacity int
}

func validID(s string) bool {
	return s != "" && len(s) <= 256 && utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool { return r <= 32 || r == 127 || r == '+' || r == '#' || r == '/' }) < 0
}
func canonical(r Reading) ([]byte, string, error) {
	for _, s := range []string{r.EventID, r.TenantID, r.GatewayID, r.DeviceID, r.PointID} {
		if !validID(s) {
			return nil, "", ErrInput
		}
	}
	if r.ObservedAt.IsZero() || r.ObservedAt.Location() != time.UTC || r.ObservedAt.Year() < 1970 || r.ObservedAt.Year() > 9999 || r.ObservedAt != r.ObservedAt.Truncate(time.Microsecond) || math.IsNaN(r.Value) || math.IsInf(r.Value, 0) || len(r.Unit) > 64 || !utf8.ValidString(r.Unit) || r.SchemaVersion != 1 {
		return nil, "", ErrInput
	}
	switch r.Quality {
	case "measured", "estimated", "missing", "stale":
	default:
		return nil, "", ErrInput
	}
	if r.Value == 0 {
		r.Value = 0
	}
	b, err := json.Marshal(r)
	if err != nil || len(b) > 8192 {
		return nil, "", ErrInput
	}
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:]), nil
}

// Open refuses before touching a path unless explicitly enabled. The separate
// database must be empty or have exactly this schema and retained capacity.
// It never migrates, opens or imports the legacy broker-ACK queue on purpose.
func Open(ctx context.Context, c Config) (*Store, error) {
	if !c.Enabled {
		return nil, ErrDisabled
	}
	if ctx == nil || c.Path == "" || !filepath.IsAbs(c.Path) || c.Capacity < 1 || c.Capacity > MaxCapacity {
		return nil, ErrInput
	}
	u := url.URL{Scheme: "file", Path: c.Path}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var tx *sql.Tx
	fail := func(e error) (*Store, error) {
		if tx != nil {
			tx.Rollback()
		}
		db.Close()
		return nil, e
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE name NOT GLOB 'sqlite_*'`)
	if err != nil {
		return fail(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			break
		}
		names = append(names, n)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return fail(err)
	}
	if len(names) == 0 {
		_, err = tx.ExecContext(ctx, metaSQL+";"+inputsSQL)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO admission_meta VALUES(1,?,?)`, Policy, c.Capacity)
		}
	} else {
		if len(names) != 2 || !((names[0] == "admission_meta" && names[1] == "admission_inputs") || (names[1] == "admission_meta" && names[0] == "admission_inputs")) {
			return fail(ErrSchema)
		}
		for name, want := range map[string]string{"admission_meta": metaSQL, "admission_inputs": inputsSQL} {
			var actual string
			if err = tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&actual); err != nil {
				return fail(err)
			}
			if actual != want {
				return fail(ErrSchema)
			}
		}
		var version, capacity, count int
		var policy string
		err = tx.QueryRowContext(ctx, `SELECT version,policy,capacity,(SELECT count(*) FROM admission_meta) FROM admission_meta`).Scan(&version, &policy, &capacity, &count)
		if err == nil && (version != 1 || policy != Policy || capacity != c.Capacity || count != 1) {
			err = ErrSchema
		}
	}
	if err != nil {
		return fail(err)
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	var mode string
	if err = db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return fail(err)
	}
	if mode != "wal" {
		return fail(ErrSchema)
	}
	if _, err = db.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
		return fail(err)
	}
	return &Store{db: db, capacity: c.Capacity}, nil
}

// Put returns a committed stable row or an exact retained duplicate. A failed
// or uncertain commit returns zero evidence. Capacity never evicts old inputs.
// A caller must visibly report failure, not silently treat a reading as saved.
func (s *Store) Put(ctx context.Context, r Reading) (Item, error) {
	if s == nil || s.db == nil {
		return Item{}, ErrDisabled
	}
	if ctx == nil {
		return Item{}, ErrInput
	}
	b, d, err := canonical(r)
	if err != nil {
		return Item{}, err
	}
	topic := "t/" + r.TenantID + "/g/" + r.GatewayID + "/telemetry"
	if len(topic) > 540 {
		return Item{}, ErrInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()
	var it Item
	var stored []byte
	var digest, storedTopic string
	err = tx.QueryRowContext(ctx, `SELECT sequence,topic,payload,digest FROM admission_inputs WHERE event_id=? AND observed_at=?`, r.EventID, r.ObservedAt.Format(time.RFC3339Nano)).Scan(&it.Sequence, &storedTopic, &stored, &digest)
	if err == nil {
		if it.Sequence < 1 {
			return Item{}, ErrEvidence
		}
		if storedTopic != topic || digest != d || !bytes.Equal(stored, b) {
			return Item{}, ErrConflict
		}
		return Item{Sequence: it.Sequence, Topic: topic, Reading: r, Payload: append([]byte(nil), b...), InputSHA256: d}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Item{}, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM admission_inputs`).Scan(&count); err != nil {
		return Item{}, err
	}
	if count >= s.capacity {
		return Item{}, ErrCapacity
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO admission_inputs(event_id,observed_at,topic,payload,digest) VALUES(?,?,?,?,?)`, r.EventID, r.ObservedAt.Format(time.RFC3339Nano), topic, b, d)
	if err != nil {
		return Item{}, err
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return Item{}, err
	}
	if err = tx.Commit(); err != nil {
		return Item{}, err
	}
	return Item{Sequence: seq, Topic: topic, Reading: r, Payload: append([]byte(nil), b...), InputSHA256: d}, nil
}

// Next revalidates retained evidence before exposing an ordered bounded batch.
// Any corruption fails the WHOLE batch; no input is silently skipped/deleted.
func (s *Store) Next(ctx context.Context, n int) ([]Item, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	if ctx == nil {
		return nil, ErrInput
	}
	if n < 1 || n > 256 {
		return nil, ErrInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,event_id,observed_at,topic,payload,digest FROM admission_inputs ORDER BY sequence LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		var event, ts string
		if err = rows.Scan(&it.Sequence, &event, &ts, &it.Topic, &it.Payload, &it.InputSHA256); err != nil {
			return nil, err
		}
		if json.Unmarshal(it.Payload, &it.Reading) != nil {
			return nil, ErrEvidence
		}
		b, d, e := canonical(it.Reading)
		if it.Sequence < 1 || e != nil || !bytes.Equal(b, it.Payload) || d != it.InputSHA256 || event != it.Reading.EventID || ts != it.Reading.ObservedAt.Format(time.RFC3339Nano) || it.Topic != "t/"+it.Reading.TenantID+"/g/"+it.Reading.GatewayID+"/telemetry" {
			return nil, ErrEvidence
		}
		out = append(out, it)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// MatchReceipt is PURE comparison, NOT authentication or an acknowledgment.
// Even a perfect forgery can match these public fields. There is intentionally
// deletion or rearm through this function. Only the separate ReconcileSigned
// path verifies a pinned signature and atomically reconciles retained evidence.
func MatchReceipt(it Item, r Receipt) error {
	b, d, err := canonical(it.Reading)
	e := it.Reading
	if err != nil || it.Sequence < 1 || !bytes.Equal(b, it.Payload) || d != it.InputSHA256 || it.Topic != "t/"+e.TenantID+"/g/"+e.GatewayID+"/telemetry" || r.Version != 1 || r.Policy != Policy || r.TenantID != e.TenantID || r.GatewayID != e.GatewayID || r.DeviceID != e.DeviceID || r.PointID != e.PointID || r.EventID != e.EventID || r.ObservedAt != e.ObservedAt || r.InputSHA256 != d || (r.New && r.State != "pending") {
		return ErrEvidence
	}
	switch r.State {
	case "pending", "executing", "outcome_unknown", "completed":
		return nil
	}
	return ErrEvidence
}
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
