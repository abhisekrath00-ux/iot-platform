// Package search indexes entities into Elasticsearch and runs tenant-scoped
// search. Best-effort: the platform works without ES; search returns unavailable
// when ES is absent; normal Postgres-backed list endpoints remain usable.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Client struct {
	base string
	hc   *http.Client
}

var indexRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

const maxSearchResponse = 1 << 20

func New(base string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), hc: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Index upserts one document into an index. Tenant is a doc field; every
// query filters on it.
func (c *Client) Index(ctx context.Context, index, id string, doc any) error {
	if c == nil || c.base == "" {
		return nil
	}
	if !indexRE.MatchString(index) || id == "" {
		return errors.New("invalid search index or document id")
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "PUT",
		fmt.Sprintf("%s/%s/_doc/%s", c.base, index, url.PathEscape(id)), bytes.NewReader(b))
	if err != nil {
		return err
	}
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
	if tenantID == "" || strings.TrimSpace(q) == "" || len(q) > 512 || len(indices) == 0 {
		return nil, errors.New("invalid search query")
	}
	for _, index := range indices {
		if !indexRE.MatchString(index) {
			return nil, errors.New("invalid search index")
		}
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
	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/%s/_search", c.base, strings.Join(indices, ",")), bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search backend status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchResponse+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxSearchResponse {
		return nil, errors.New("search response too large")
	}
	var out struct {
		Hits *struct {
			Hits []struct {
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out.Hits == nil || out.Hits.Hits == nil || len(out.Hits.Hits) > 25 {
		return nil, errors.New("invalid search response shape or hit count")
	}
	hits := make([]json.RawMessage, 0, len(out.Hits.Hits))
	for _, h := range out.Hits.Hits {
		var source struct {
			Tenant string `json:"tenant_id"`
		}
		if json.Unmarshal(h.Source, &source) != nil || source.Tenant != tenantID {
			return nil, errors.New("search result tenant mismatch")
		}
		hits = append(hits, h.Source)
	}
	return hits, nil
}
