// Package indexsink copies telemetry into an OpenSearch or Elasticsearch index for people who already run one.
// It is OFF unless INDEX_SINK_URL is set, never needed by the platform, and the database stays the source of
// truth. It only reads telemetry and only talks to the URL the operator configured.
//
// Delivery is at-least-once and idempotent: documents use event_id as the _id, so a retry overwrites. The cursor
// (received_at, event_id) advances only after the whole bulk request was accepted item by item.
// Rows are read only once they are older than Lag, so a transaction that commits slightly late is not skipped.
package indexsink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	URL, Index          string
	User, Password      string
	APIKey              string
	Batch               int
	Lag                 time.Duration
	AllowInsecureRemote bool
	Name                string
}

// FromEnv returns nil when the sink is not configured (the default).
func FromEnv(get func(string) string) (*Config, error) {
	u := strings.TrimSpace(get("INDEX_SINK_URL"))
	if u == "" {
		return nil, nil
	}
	c := &Config{URL: strings.TrimRight(u, "/"), Index: get("INDEX_SINK_INDEX"), User: get("INDEX_SINK_USER"), Password: get("INDEX_SINK_PASSWORD"),
		APIKey: get("INDEX_SINK_API_KEY"), Batch: 500, Lag: 5 * time.Second, AllowInsecureRemote: get("INDEX_SINK_ALLOW_INSECURE") == "1", Name: "default"}
	if c.Index == "" {
		c.Index = "hexmon-telemetry"
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate refuses credentials sent over plain http to a non-local host unless the operator opted in.
func (c *Config) Validate() error {
	pu, err := url.Parse(c.URL)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
		return errors.New("INDEX_SINK_URL must be an http(s) URL")
	}
	if strings.ContainsAny(c.Index, " /\\,*?\"<>|#:") || strings.HasPrefix(c.Index, "_") || strings.HasPrefix(c.Index, "-") || c.Index != strings.ToLower(c.Index) {
		return errors.New("INDEX_SINK_INDEX must be a lowercase index name")
	}
	if pu.Scheme == "http" && !c.AllowInsecureRemote && !isLocal(pu.Hostname()) {
		return errors.New("INDEX_SINK_URL uses plain http to a non-local host; use https or set INDEX_SINK_ALLOW_INSECURE=1")
	}
	return nil
}

func isLocal(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

type doc struct {
	TenantID   string    `json:"tenant_id"`
	SiteID     *string   `json:"site_id,omitempty"`
	GatewayID  string    `json:"gateway_id"`
	DeviceID   string    `json:"device_id"`
	PointID    string    `json:"point_id"`
	ObservedAt time.Time `json:"@timestamp"`
	ReceivedAt time.Time `json:"received_at"`
	Value      float64   `json:"value"`
	Unit       string    `json:"unit"`
	Quality    string    `json:"quality"`
}

// Sink pushes one batch per Step call.
type Sink struct {
	C  *Config
	DB *pgxpool.Pool
	HC *http.Client
}

func New(c *Config, db *pgxpool.Pool) *Sink {
	return &Sink{C: c, DB: db, HC: &http.Client{Timeout: 20 * time.Second}}
}

// Step sends at most one batch. It returns how many documents were accepted. A failure is stored in
// index_sink_cursor.last_error and the cursor does not move.
func (s *Sink) Step(ctx context.Context) (int, error) {
	n, err := s.step(ctx)
	msg := ""
	if err != nil {
		msg = err.Error()
		if len(msg) > 300 {
			msg = msg[:300]
		}
	}
	s.DB.Exec(ctx, `INSERT INTO index_sink_cursor(name,received_at,event_id,last_error) VALUES($1,'epoch','',$2)
		ON CONFLICT (name) DO UPDATE SET last_error=$2, updated_at=now()`, s.C.Name, msg)
	return n, err
}

func (s *Sink) step(ctx context.Context) (int, error) {
	var at time.Time
	var eid string
	err := s.DB.QueryRow(ctx, `SELECT received_at,event_id FROM index_sink_cursor WHERE name=$1`, s.C.Name).Scan(&at, &eid)
	if errors.Is(err, pgx.ErrNoRows) {
		at, eid = time.Unix(0, 0).UTC(), ""
	} else if err != nil {
		return 0, err
	}
	rows, err := s.DB.Query(ctx, `SELECT event_id,tenant_id,site_id,gateway_id,device_id,point_id,observed_at,received_at,value,unit,quality
		FROM telemetry WHERE (received_at,event_id) > ($1,$2) AND received_at < now() - make_interval(secs => $4)
		ORDER BY received_at,event_id LIMIT $3`, at, eid, s.C.Batch, s.C.Lag.Seconds())
	if err != nil {
		return 0, err
	}
	var body bytes.Buffer
	var ids []string
	var lastAt time.Time
	var lastID string
	for rows.Next() {
		var id string
		var d doc
		if err := rows.Scan(&id, &d.TenantID, &d.SiteID, &d.GatewayID, &d.DeviceID, &d.PointID, &d.ObservedAt, &d.ReceivedAt, &d.Value, &d.Unit, &d.Quality); err != nil {
			rows.Close()
			return 0, err
		}
		meta, _ := json.Marshal(map[string]any{"index": map[string]any{"_index": s.C.Index, "_id": id}})
		b, _ := json.Marshal(d)
		body.Write(meta)
		body.WriteByte('\n')
		body.Write(b)
		body.WriteByte('\n')
		ids = append(ids, id)
		lastAt, lastID = d.ReceivedAt, id
	}
	rows.Close()
	if rows.Err() != nil || len(ids) == 0 {
		return 0, rows.Err()
	}
	if err := s.bulk(ctx, body.Bytes(), len(ids)); err != nil {
		return 0, err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO index_sink_cursor(name,received_at,event_id,pushed,last_error) VALUES($1,$2,$3,$4,'')
		ON CONFLICT (name) DO UPDATE SET received_at=$2, event_id=$3, pushed=index_sink_cursor.pushed+$4, last_error='', updated_at=now()`,
		s.C.Name, lastAt, lastID, len(ids))
	return len(ids), err
}

func (s *Sink) bulk(ctx context.Context, body []byte, want int) error {
	req, _ := http.NewRequestWithContext(ctx, "POST", s.C.URL+"/_bulk", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-ndjson")
	if s.C.APIKey != "" {
		req.Header.Set("Authorization", "ApiKey "+s.C.APIKey)
	} else if s.C.User != "" {
		req.SetBasicAuth(s.C.User, s.C.Password)
	}
	resp, err := s.HC.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bulk: http %d", resp.StatusCode)
	}
	var out struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int `json:"status"`
			Error  any `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("bulk: unreadable response")
	}
	if len(out.Items) != want {
		return fmt.Errorf("bulk: %d results for %d documents", len(out.Items), want)
	}
	for _, it := range out.Items {
		for _, r := range it {
			if r.Status >= 300 || r.Error != nil {
				return fmt.Errorf("bulk: an item was rejected (status %d)", r.Status)
			}
		}
	}
	return nil
}

// Run pushes batches until ctx ends, backing off after an error.
func (s *Sink) Run(ctx context.Context) {
	wait := 2 * time.Second
	for ctx.Err() == nil {
		n, err := s.Step(ctx)
		switch {
		case err != nil:
			wait = min(wait*2, time.Minute)
		case n < s.C.Batch:
			wait = 5 * time.Second
		default:
			wait = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
