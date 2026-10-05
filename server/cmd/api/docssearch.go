package main

import (
	"net/http"
	"strings"
	"sync"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/docsrag"
)

var (
	docsOnce sync.Once
	docsIdx  *docsrag.Index
)

func docsIndex() *docsrag.Index {
	docsOnce.Do(func() { docsIdx, _ = docsrag.Embedded() })
	return docsIdx
}

// GET /v1/docs/search?q=...: passages of the platform's own documentation that match a question,
// each with the document and heading it came from. Product documentation only: it holds no tenant
// data, so any signed-in user may search it. Passages are text to read, never instructions.
func (s *server) searchDocs(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > 300 {
		http.Error(w, "q is required, up to 300 characters", 400)
		return
	}
	ix := docsIndex()
	if ix == nil {
		http.Error(w, "the documentation index is not available", 503)
		return
	}
	hits := ix.Search(q, 4)
	if hits == nil {
		hits = []docsrag.Hit{}
	}
	writeJSON(w, 200, map[string]any{"query": q, "results": hits, "note": "Quote the doc and heading when you answer from these. If nothing here answers the question, say the documentation does not cover it."})
}
