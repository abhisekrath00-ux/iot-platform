// Package queue is the edge store-and-forward buffer (SQLite). Telemetry
// survives WAN loss, broker restarts and gateway reboots; rows are deleted
// only after the broker acknowledges publish.
package queue

import (
	"context"
	"database/sql"

	_ "modernc.org/sqlite"
)

// DefaultMaxRows bounds the outbox. During a very long outage the oldest readings are dropped
// rather than filling the gateway's disk (about 1M small readings is on the order of 200 MB).
const DefaultMaxRows = 1_000_000

type Queue struct {
	db      *sql.DB
	MaxRows int // 0 = DefaultMaxRows
	puts    int
	Dropped int64
}

type Item struct {
	ID      int64
	Topic   string
	Payload []byte
}

func Open(path string) (*Queue, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS outbox(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		topic TEXT NOT NULL,
		payload BLOB NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`)
	if err != nil {
		return nil, err
	}
	return &Queue{db: db}, nil
}

func (q *Queue) Put(ctx context.Context, topic string, payload []byte) error {
	_, err := q.db.ExecContext(ctx, `INSERT INTO outbox(topic,payload) VALUES(?,?)`, topic, payload)
	if err != nil {
		return err
	}
	q.puts++
	if q.puts%500 == 0 { // check the bound every 500 puts, cheaply
		max := q.MaxRows
		if max <= 0 {
			max = DefaultMaxRows
		}
		if res, e := q.db.ExecContext(ctx, `DELETE FROM outbox WHERE id <= (SELECT max(id) FROM outbox) - ?`, max); e == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				q.Dropped += n
			}
		}
	}
	return nil
}

// Next returns up to n oldest items.
func (q *Queue) Next(ctx context.Context, n int) ([]Item, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT id,topic,payload FROM outbox ORDER BY id LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Topic, &it.Payload); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (q *Queue) Ack(ctx context.Context, id int64) error {
	_, err := q.db.ExecContext(ctx, `DELETE FROM outbox WHERE id=?`, id)
	return err
}

// Depth reports buffered item count; alarming on growth is an ops requirement.
func (q *Queue) Depth(ctx context.Context) (int, error) {
	var n int
	err := q.db.QueryRowContext(ctx, `SELECT count(*) FROM outbox`).Scan(&n)
	return n, err
}

func (q *Queue) Close() error { return q.db.Close() }
