package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationAssetAttributes(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-aa1")
	seed(t, s, "itest-aa2")
	ctx := t.Context()
	clean := func() {
		for _, tn := range []string{"itest-aa1", "itest-aa2"} {
			s.st.Pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id=$1`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM attribute_defs WHERE tenant_id=$1`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id=$1`, tn)
		}
	}
	clean()
	t.Cleanup(clean)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/assets", s.listAssets)
	api.HandleFunc("POST /v1/assets", s.createAsset)
	api.HandleFunc("PUT /v1/assets/{id}/attributes", s.setAssetAttributes)
	api.HandleFunc("PUT /v1/devices/{id}/attributes", s.setDeviceAttributes)
	api.HandleFunc("PUT /v1/attribute-defs/{key}", s.putAttrDef)
	api.HandleFunc("GET /v1/attribute-defs", s.listAttrDefs)
	w := call(api, "itest-aa1", "operator", "POST", "/v1/assets", `{"name":"Boiler","kind":"machine"}`)
	var a struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &a)
	if a.ID == "" {
		t.Fatalf("asset not created: %s", w.Body)
	}
	def := func(key, body string) {
		if w := call(api, "itest-aa1", "admin", "PUT", "/v1/attribute-defs/"+key, body); w.Code != 200 {
			t.Fatalf("def %s: %d %s", key, w.Code, w.Body)
		}
	}
	def("rated_kw", `{"type":"number","unit":"kW","required":true,"applies_to":"asset"}`)
	def("owner", `{"type":"string","applies_to":"both"}`)
	def("firmware", `{"type":"string","required":true}`) // device only, default
	if w := call(api, "itest-aa1", "admin", "PUT", "/v1/attribute-defs/x", `{"type":"string","applies_to":"galaxy"}`); w.Code != 400 {
		t.Fatalf("bad applies_to = %d", w.Code)
	}
	put := func(tenant, role, id, body string) int {
		return call(api, tenant, role, "PUT", "/v1/assets/"+id+"/attributes", body).Code
	}
	if put("itest-aa1", "viewer", a.ID, `{"attributes":{"rated_kw":5}}`) != 403 {
		t.Fatal("viewer wrote asset attributes")
	}
	if put("itest-aa1", "operator", a.ID, `{"attributes":{"owner":"ops"}}`) != 400 {
		t.Fatal("missing required asset attribute accepted")
	}
	if put("itest-aa1", "operator", a.ID, `{"attributes":{"rated_kw":"big"}}`) != 400 {
		t.Fatal("wrong type accepted")
	}
	// device-only 'firmware' is required for devices but not for assets
	if c := put("itest-aa1", "operator", a.ID, `{"attributes":{"rated_kw":75.5,"owner":"ops"}}`); c != 200 {
		t.Fatalf("valid asset attributes = %d", c)
	}
	if put("itest-aa2", "operator", a.ID, `{"attributes":{"rated_kw":1}}`) != 404 {
		t.Fatal("another tenant wrote the asset")
	}
	if c := call(api, "itest-aa1", "operator", "PUT", "/v1/devices/itest-aa1-dev/attributes", `{"attributes":{"firmware":"1.2","owner":"x"}}`).Code; c != 200 {
		t.Fatalf("device attributes must ignore asset-only definitions: %d", c)
	}
	if call(api, "itest-aa1", "operator", "PUT", "/v1/devices/itest-aa1-dev/attributes", `{"attributes":{"firmware":"1","rated_kw":3}}`).Code != 200 {
		// free-form key with an asset-only definition is not held to it on a device
		t.Fatal("device rejected a key defined only for assets")
	}
	l := call(api, "itest-aa1", "viewer", "GET", "/v1/assets", "")
	if !strings.Contains(l.Body.String(), `"rated_kw":75.5`) {
		t.Fatalf("list lacks attributes: %s", l.Body)
	}
	if d := call(api, "itest-aa1", "viewer", "GET", "/v1/attribute-defs?for=device", ""); strings.Contains(d.Body.String(), "rated_kw") || !strings.Contains(d.Body.String(), "owner") {
		t.Fatalf("for=device filter wrong: %s", d.Body)
	}
}
