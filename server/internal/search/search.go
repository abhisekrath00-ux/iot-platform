// Package search indexes entities into Elasticsearch and runs tenant-scoped
// search. Best-effort: the platform works without ES; search degrades to
// Postgres-backed listing when ES is absent.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	base string
	hc   *http.Client
}

func New(base string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), hc: &http.Client{Timeout: 8 * time.Second}}
}

// Index upserts one document into an index. Tenant is a doc field; every
// query filters on it.
func (c *Client) Index(ctx context.Context, index, id string, doc any) error {
	if c == nil || c.base == "" {
		return nil
	}
	b, _ := json.Marshal(doc)
	req, _ := http.NewRequestWithContext(ctx, "PUT",
		fmt.Sprintf("%s/%s/_doc/%s", c.base, index, id), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("es index: %d %s", resp.StatusCode, body)
	}
	return nil
}

// Query runs a tenant-scoped multi-match search and returns raw hits.
func (c *Client) Query(ctx context.Context, indices []string, tenantID, q string) ([]json.RawMessage, error) {
	if c == nil || c.base == "" {
		return nil, fmt.Errorf("search unavailable")
	}
	body := map[string]any{
		"size": 25,
		"query": map[string]any{
			"bool": map[string]any{
				"must":   map[string]any{"multi_match": map[string]any{"query": q, "fields": []string{"name^3", "message^2", "profile", "id"}}},
				"filter": map[string]any{"term": map[string]any{"tenant_id": tenantID}},
			},
		},
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/%s/_search", c.base, strings.Join(indices, ",")), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Hits struct {
			Hits []struct {
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	hits := make([]json.RawMessage, 0, len(out.Hits.Hits))
	for _, h := range out.Hits.Hits {
		hits = append(hits, h.Source)
	}
	return hits, nil
}
