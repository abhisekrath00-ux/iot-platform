package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestIntegrationPreviewDoesNotCallOldOrEstimatedDataLive(t *testing.T) {
	s, _ := testServer(t)
	tenant := "itest-preview-fresh"
	seed(t, s, tenant)
	_, err := s.st.Pool.Exec(t.Context(), `INSERT INTO commissioning_sessions(id,tenant_id,site_id,gateway_id,serial,device_id,created_by) VALUES('fresh-preview',$1,$2,$3,'serial',$4,'test-user')`, tenant, tenant+"-site", tenant+"-gw", tenant+"-dev")
	if err != nil {
		t.Fatal(err)
	}
	m := http.NewServeMux()
	m.HandleFunc("GET /v1/commissioning/sessions/{id}/preview", s.commissionPreview)
	check := func(want bool) {
		w := call(m, tenant, "admin", "GET", "/v1/commissioning/sessions/fresh-preview/preview", "")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		var out struct {
			Live   bool             `json:"live"`
			Points []map[string]any `json:"points"`
		}
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Live != want {
			t.Fatalf("expected live=%v %s", want, w.Body)
		}
	}
	s.st.Pool.Exec(t.Context(), `UPDATE telemetry SET observed_at=now()-interval '1 day' WHERE tenant_id=$1`, tenant)
	check(false)
	s.st.Pool.Exec(t.Context(), `UPDATE telemetry SET observed_at=now(),quality='estimated' WHERE tenant_id=$1`, tenant)
	check(false)
	s.st.Pool.Exec(t.Context(), `UPDATE telemetry SET observed_at=now()+interval '1 hour',quality='measured' WHERE tenant_id=$1`, tenant)
	check(false)
	s.st.Pool.Exec(t.Context(), `UPDATE telemetry SET observed_at=now(),quality='measured' WHERE tenant_id=$1`, tenant)
	check(true)
	if got := call(m, "foreign-preview", "admin", "GET", "/v1/commissioning/sessions/fresh-preview/preview", "").Code; got != 404 {
		t.Fatal("foreign", got)
	}
}
