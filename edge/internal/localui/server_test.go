package localui

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/admissionoutbox"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestStagedAdmissionReadOnlyOptIn(t *testing.T) {
	calls := 0
	probe := func(ctx context.Context) (admissionoutbox.RecoveryStatus, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("no deadline")
		}
		return admissionoutbox.RecoveryStatus{AsOf: time.Now().UTC(), Collection: admissionoutbox.CollectorStatus{State: "open", Pending: 2}, Session: admissionoutbox.SessionStatus{State: "open"}}, nil
	}
	for _, enabled := range []bool{false, true} {
		info := Info{}
		if enabled {
			info.StagedAdmission = probe
		}
		h := Handler(info, NewTracker())
		before := calls
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, "/api/admission-status", nil))
			if rec.Code != 405 || calls != before {
				t.Fatal(rec.Code, calls)
			}
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/admission-status", nil))
		if enabled {
			var d admissionoutbox.RecoveryStatus
			if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &d) != nil || d.Collection.Pending != 2 || calls != before+1 {
				t.Fatal(rec.Code, rec.Body.String(), calls)
			}
		} else if rec.Code != 404 || calls != before {
			t.Fatal(rec.Code, calls)
		}
		if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal(rec.Header())
		}
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		marker := `data-admission="disabled"`
		if enabled {
			marker = `data-admission="enabled"`
		}
		if !strings.Contains(rec.Body.String(), marker) {
			t.Fatal("wrong opt-in marker")
		}
	}
	h := Handler(Info{StagedAdmission: func(context.Context) (admissionoutbox.RecoveryStatus, error) {
		return admissionoutbox.RecoveryStatus{Session: admissionoutbox.SessionStatus{Capacity: &admissionoutbox.CapacityStatus{Retained: 99}}}, errors.New("secret endpoint password")
	}}, NewTracker())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/admission-status", nil))
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "99") || strings.Contains(rec.Body.String(), "secret") {
		t.Fatal(rec.Code, rec.Body.String())
	}
}
