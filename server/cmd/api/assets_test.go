package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestIntegrationAssetHierarchy(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-as1")
	seed(t, s, "itest-as2")
	ctx := t.Context()
	clean := func() {
		for _, tn := range []string{"itest-as1", "itest-as2"} {
			s.st.Pool.Exec(ctx, `UPDATE devices SET asset_id=NULL WHERE tenant_id=$1`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id=$1 AND parent_id IS NOT NULL`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id=$1`, tn)
		}
	}
	clean()
	t.Cleanup(clean)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/assets", s.listAssets)
	api.HandleFunc("POST /v1/assets", s.createAsset)
	api.HandleFunc("DELETE /v1/assets/{id}", s.deleteAsset)
	api.HandleFunc("PUT /v1/devices/{id}/asset", s.setDeviceAsset)
	create := func(tenant, body string) (int, string) {
		w := call(api, tenant, "operator", "POST", "/v1/assets", body)
		var o map[string]any
		json.Unmarshal(w.Body.Bytes(), &o)
		id, _ := o["id"].(string)
		return w.Code, id
	}
	code, plant := create("itest-as1", `{"name":"Plant 1","kind":"plant"}`)
	if code != 201 {
		t.Fatalf("root %d", code)
	}
	code, line := create("itest-as1", `{"name":"Line A","kind":"line","parent_id":"`+plant+`"}`)
	if code != 201 {
		t.Fatalf("child %d", code)
	}
	if c, _ := create("itest-as2", `{"name":"x","parent_id":"`+plant+`"}`); c != 404 {
		t.Fatalf("cross-tenant parent %d", c)
	}
	if c, _ := create("itest-as1", `{"name":"<b>"}`); c != 400 {
		t.Fatalf("bad name %d", c)
	}
	// depth cap
	parent := line
	for i := 0; i < maxAssetDepth+2; i++ {
		c, id := create("itest-as1", `{"name":"deep","parent_id":"`+parent+`"}`)
		if c == 400 {
			break
		}
		if c != 201 {
			t.Fatalf("deep %d", c)
		}
		parent = id
		if i == maxAssetDepth+1 {
			t.Fatal("depth cap never triggered")
		}
	}
	// attach device, then deletion of busy asset is refused
	if w := call(api, "itest-as1", "operator", "PUT", "/v1/devices/itest-as1-dev/asset", `{"asset_id":"`+line+`"}`); w.Code != 200 {
		t.Fatalf("attach %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-as2", "operator", "PUT", "/v1/devices/itest-as1-dev/asset", `{"asset_id":"`+line+`"}`); w.Code != 404 {
		t.Fatalf("cross-tenant attach %d", w.Code)
	}
	if w := call(api, "itest-as1", "operator", "DELETE", "/v1/assets/"+line, ""); w.Code != 409 {
		t.Fatalf("delete busy %d", w.Code)
	}
	w := call(api, "itest-as1", "viewer", "GET", "/v1/assets", "")
	var list []map[string]any
	json.Unmarshal(w.Body.Bytes(), &list)
	var lineDevices float64 = -1
	for _, a := range list {
		if a["id"] == line {
			lineDevices = a["devices"].(float64)
		}
	}
	if lineDevices != 1 {
		t.Fatalf("device count on Line A = %v", lineDevices)
	}
	call(api, "itest-as1", "operator", "PUT", "/v1/devices/itest-as1-dev/asset", `{"asset_id":null}`)
	if call(api, "itest-as1", "viewer", "POST", "/v1/assets", `{"name":"v"}`).Code != 403 {
		t.Fatal("viewer created asset")
	}
}

func TestIntegrationDeviceFilterByAsset(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-af1")
	seed(t, s, "itest-af2")
	ctx := t.Context()
	clean := func() {
		for _, tn := range []string{"itest-af1", "itest-af2"} {
			s.st.Pool.Exec(ctx, `UPDATE devices SET asset_id=NULL WHERE tenant_id=$1`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id=$1 AND parent_id IS NOT NULL`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id=$1`, tn)
		}
	}
	clean()
	t.Cleanup(clean)
	s.st.Pool.Exec(ctx, `INSERT INTO assets(id,tenant_id,parent_id,name,kind) VALUES('af-plant','itest-af1',NULL,'P','plant'),('af-other','itest-af2',NULL,'O','plant')`)
	s.st.Pool.Exec(ctx, `INSERT INTO assets(id,tenant_id,parent_id,name,kind) VALUES('af-line','itest-af1','af-plant','L','line')`)
	s.st.Pool.Exec(ctx, `UPDATE devices SET asset_id='af-line' WHERE id='itest-af1-dev'`)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/devices", s.listDevices)
	ids := func(tenant, q string) []string {
		w := call(api, tenant, "viewer", "GET", "/v1/devices"+q, "")
		var l []map[string]any
		json.Unmarshal(w.Body.Bytes(), &l)
		var out []string
		for _, d := range l {
			out = append(out, d["id"].(string))
		}
		return out
	}
	if got := ids("itest-af1", "?asset_id=af-plant"); len(got) != 1 || got[0] != "itest-af1-dev" {
		t.Fatalf("descendant match: %v", got)
	}
	if got := ids("itest-af1", "?asset_id=af-other"); len(got) != 0 {
		t.Fatalf("other tenant's asset must match nothing: %v", got)
	}
	s.st.Pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-af1' AND id LIKE 'af-al%'`)
	s.st.Pool.Exec(ctx, `INSERT INTO alerts(id,tenant_id,severity,message,device_id) VALUES('af-al1','itest-af1','warning','on line','itest-af1-dev'),('af-al2','itest-af1','warning','no device',NULL)`)
	t.Cleanup(func() { s.st.Pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-af1' AND id LIKE 'af-al%'`) })
	api.HandleFunc("GET /v1/alerts", s.listAlerts)
	alertIDs := func(tenant, q string) int {
		w := call(api, tenant, "viewer", "GET", "/v1/alerts"+q, "")
		var l []map[string]any
		json.Unmarshal(w.Body.Bytes(), &l)
		return len(l)
	}
	if n := alertIDs("itest-af1", "?asset_id=af-plant"); n != 1 {
		t.Fatalf("alerts under asset: %d", n)
	}
	if n := alertIDs("itest-af2", "?asset_id=af-plant"); n != 0 {
		t.Fatalf("cross-tenant alerts: %d", n)
	}
	if got := ids("itest-af1", ""); len(got) == 0 {
		t.Fatal("unfiltered list empty")
	}
}
