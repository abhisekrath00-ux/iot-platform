package explorer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSource struct {
	calls atomic.Int32
	read  func(context.Context, Range) ([]uint16, error)
}

func (f *fakeSource) Connections() []Connection {
	return []Connection{{ID: "owned-v1", Name: "Synthetic meter", Transport: "rtu", Endpoint: "fake-port", Unit: 7}}
}
func (f *fakeSource) Read(c context.Context, r Range) ([]uint16, error) {
	f.calls.Add(1)
	if f.read != nil {
		return f.read(c, r)
	}
	return make([]uint16, r.Count), nil
}
func fixture(t *testing.T, f *fakeSource) *Server {
	t.Helper()
	s, err := New("127.0.0.1:19191", f)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func request(s *Server, method, path, body string) *http.Request {
	r := httptest.NewRequest(method, s.origin+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Explorer-Session", s.token)
	r.Header.Set("Content-Type", "application/json")
	if method == "POST" {
		r.Header.Set("Origin", s.origin)
	}
	return r
}

const validBody = `{"connection":"owned-v1","function":3,"offset":0,"count":2}`

func TestExplorerGuardsBeforeSource(t *testing.T) {
	cases := []struct {
		name, method, path, body string
		change                   func(*http.Request)
		want                     int
	}{
		{"foreign host", "POST", "/api/read", validBody, func(r *http.Request) { r.Host = "attacker.invalid:19191" }, 403},
		{"LAN peer", "POST", "/api/read", validBody, func(r *http.Request) { r.RemoteAddr = "192.168.1.4:54321" }, 403},
		{"foreign origin", "POST", "/api/read", validBody, func(r *http.Request) { r.Header.Set("Origin", "https://foreign.invalid") }, 403},
		{"same site not origin", "POST", "/api/read", validBody, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }, 403},
		{"no origin", "POST", "/api/read", validBody, func(r *http.Request) { r.Header.Del("Origin") }, 403},
		{"no capability", "POST", "/api/read", validBody, func(r *http.Request) { r.Header.Del("X-Explorer-Session") }, 403},
		{"wrong capability", "POST", "/api/read", validBody, func(r *http.Request) { r.Header.Set("X-Explorer-Session", "other") }, 403},
		{"simple form", "POST", "/api/read", validBody, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"GET cannot read", "GET", "/api/read", "", nil, 405},
		{"unknown path", "POST", "/api/write", validBody, nil, 404},
		{"query", "POST", "/api/read?x=1", validBody, nil, 400},
		{"write function", "POST", "/api/read", `{"connection":"a","function":6,"offset":0,"count":1}`, nil, 400},
		{"unknown field", "POST", "/api/read", strings.TrimSuffix(validBody, "}") + `,"host":"other"}`, nil, 400},
		{"second JSON", "POST", "/api/read", validBody + validBody, nil, 400},
		{"too large", "POST", "/api/read", strings.Repeat(" ", 2050) + validBody, nil, 400},
		{"negative offset", "POST", "/api/read", `{"connection":"a","function":3,"offset":-1,"count":1}`, nil, 400},
		{"range overflow", "POST", "/api/read", `{"connection":"a","function":3,"offset":65535,"count":2}`, nil, 400},
		{"zero count", "POST", "/api/read", `{"connection":"a","function":3,"offset":0,"count":0}`, nil, 400},
		{"oversize count", "POST", "/api/read", `{"connection":"a","function":3,"offset":0,"count":33}`, nil, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSource{}
			s := fixture(t, f)
			r := request(s, tc.method, tc.path, tc.body)
			if tc.change != nil {
				tc.change(r)
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tc.want || f.calls.Load() != 0 {
				t.Fatalf("status %d wanted %d, source calls %d: %s", w.Code, tc.want, f.calls.Load(), w.Body)
			}
		})
	}
}
func TestPageAndInventoryNeverRead(t *testing.T) {
	f := &fakeSource{}
	s := fixture(t, f)
	for _, p := range []string{"/", "/api/connections"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, request(s, "GET", p, ""))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatal("missing browser guards")
		}
		if p == "/" && (!strings.Contains(w.Body.String(), s.token) || strings.Contains(w.Body.String(), "SESSION_TOKEN")) {
			t.Fatal("session not injected")
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("viewing triggered read")
	}
}
func TestReadCorrelationAllFunctionsAndCountFence(t *testing.T) {
	for fn := 1; fn <= 4; fn++ {
		f := &fakeSource{}
		s := fixture(t, f)
		body, _ := json.Marshal(Range{"owned-v1", fn, 65504, 32})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, request(s, "POST", "/api/read", string(body)))
		var got reply
		if json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Function != fn || got.Offset != 65504 || got.Count != 32 || len(got.Words) != 32 || got.Error != "" || got.At.IsZero() {
			t.Fatal(w.Body.String())
		}
	}
	for _, words := range [][]uint16{{1}, {0, 0, 0}, {0, 2}} {
		f := &fakeSource{read: func(context.Context, Range) ([]uint16, error) { return words, nil }}
		s := fixture(t, f)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, request(s, "POST", "/api/read", strings.Replace(validBody, `"function":3`, `"function":1`, 1)))
		var got reply
		json.Unmarshal(w.Body.Bytes(), &got)
		if got.Error == "" || len(got.Words) != 0 {
			t.Fatal("invalid data accepted", w.Body)
		}
	}
}
func TestOnlyOneReadNoQueue(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	f := &fakeSource{read: func(context.Context, Range) ([]uint16, error) { close(entered); <-release; return []uint16{1, 2}, nil }}
	s := fixture(t, f)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.ServeHTTP(httptest.NewRecorder(), request(s, "POST", "/api/read", validBody))
	}()
	<-entered
	w := httptest.NewRecorder()
	s.ServeHTTP(w, request(s, "POST", "/api/read", validBody))
	if w.Code != 409 || f.calls.Load() != 1 {
		t.Fatal(w.Code, f.calls.Load())
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("read stuck")
	}
}
func TestCancelledReadCannotPublishValues(t *testing.T) {
	f := &fakeSource{read: func(ctx context.Context, _ Range) ([]uint16, error) { <-ctx.Done(); return []uint16{1, 2}, nil }}
	s := fixture(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := request(s, "POST", "/api/read", validBody).WithContext(ctx)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var got reply
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.Error == "" || len(got.Words) != 0 {
		t.Fatal("cancelled sample accepted", w.Body)
	}
}
func TestNoWildcardOrDNSListener(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8089", "localhost:8089", "192.168.1.2:8089", ":8089", "[::]:8089", "127.0.0.1:0"} {
		if _, err := New(addr, &fakeSource{}); err == nil {
			t.Fatal("unsafe listener", addr)
		}
	}
	if _, err := New("[::1]:8089", &fakeSource{}); err != nil {
		t.Fatal(err)
	}
}
