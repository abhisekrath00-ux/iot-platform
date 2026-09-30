package localui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusAndReadOnly(t *testing.T) {
	tr := NewTracker()
	tr.Register("m1", "modbus-tcp")
	tr.RecordRead("m1", "modbus-tcp", map[string]float64{"kwh": 12.5})
	tr.RecordError("d2", "opcua", errors.New("dial refused"))
	h := Handler(Info{GatewayID: "gw1", TenantID: "t1", Version: "v", Connected: func() bool { return true },
		QueueDepth: func(context.Context) (int, error) { return 7, nil }}, tr)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/status", nil))
	var d statusDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if !d.Connected || d.QueueDepth != 7 || len(d.Devices) != 2 || d.Devices[1].LastValue["kwh"] != 12.5 {
		t.Fatalf("bad doc: %+v", d)
	}
	if d.Devices[0].LastError != "dial refused" {
		t.Fatalf("error not recorded: %+v", d.Devices)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/status", strings.NewReader("x")))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST must be rejected, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("index/CSP wrong: %d", rec.Code)
	}
}
