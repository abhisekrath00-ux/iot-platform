package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCheckAttrDefs(t *testing.T) {
	defs := map[string]attrDef{
		"serial": {Key: "serial", Type: "string", Required: true},
		"zone":   {Key: "zone", Type: "enum", EnumValues: []string{"A", "B"}},
		"rated":  {Key: "rated", Type: "number"},
		"spare":  {Key: "spare", Type: "boolean"},
	}
	ok := map[string]any{"serial": "X1", "zone": "A", "rated": 4.5, "spare": true, "free": "form"}
	if err := checkAttrDefs(defs, ok); err != nil {
		t.Fatal(err)
	}
	for name, a := range map[string]map[string]any{
		"missing required": {"zone": "A"},
		"bad enum":         {"serial": "x", "zone": "C"},
		"enum not string":  {"serial": "x", "zone": 1.0},
		"text as number":   {"serial": "x", "rated": "4"},
		"number as text":   {"serial": 5.0},
		"text as bool":     {"serial": "x", "spare": "yes"},
	} {
		if checkAttrDefs(defs, a) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestIntegrationGroupsAndAttrDefs(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-gr")
	seed(t, s, "itest-gr2")
	ctx := t.Context()
	clean := func() {
		s.st.Pool.Exec(ctx, `DELETE FROM device_groups WHERE tenant_id IN ('itest-gr','itest-gr2')`)
		s.st.Pool.Exec(ctx, `DELETE FROM attribute_defs WHERE tenant_id IN ('itest-gr','itest-gr2')`)
	}
	clean()
	t.Cleanup(clean)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/groups", s.listGroups)
	mux.HandleFunc("POST /v1/groups", s.createGroup)
	mux.HandleFunc("GET /v1/groups/{id}", s.getGroup)
	mux.HandleFunc("DELETE /v1/groups/{id}", s.deleteGroup)
	mux.HandleFunc("PUT /v1/groups/{id}/devices", s.setGroupDevices)
	mux.HandleFunc("GET /v1/devices", s.listDevices)
	mux.HandleFunc("PUT /v1/attribute-defs/{key}", s.putAttrDef)
	mux.HandleFunc("GET /v1/attribute-defs", s.listAttrDefs)
	mux.HandleFunc("DELETE /v1/attribute-defs/{key}", s.deleteAttrDef)
	mux.HandleFunc("PUT /v1/devices/{id}/attributes", s.setDeviceAttributes)

	// groups
	w := call(mux, "itest-gr", "operator", "POST", "/v1/groups", `{"name":"Boiler house"}`)
	var g struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &g)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if call(mux, "itest-gr", "operator", "POST", "/v1/groups", `{"name":"Boiler house"}`).Code != 409 || call(mux, "itest-gr", "viewer", "POST", "/v1/groups", `{"name":"x"}`).Code != 403 {
		t.Fatal("duplicate or viewer create accepted")
	}
	if call(mux, "itest-gr", "operator", "PUT", "/v1/groups/"+g.ID+"/devices", `{"device_ids":["itest-gr-dev","nope"]}`).Code != 400 {
		t.Fatal("unknown device accepted")
	}
	if call(mux, "itest-gr2", "admin", "PUT", "/v1/groups/"+g.ID+"/devices", `{"device_ids":["itest-gr2-dev"]}`).Code != 404 {
		t.Fatal("another tenant reached the group")
	}
	if w := call(mux, "itest-gr", "operator", "PUT", "/v1/groups/"+g.ID+"/devices", `{"device_ids":["itest-gr-dev","itest-gr-dev"]}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"devices":1`) {
		t.Fatalf("set: %d %s", w.Code, w.Body.String())
	}
	if w := call(mux, "itest-gr", "viewer", "GET", "/v1/devices?group_id="+g.ID, ""); !strings.Contains(w.Body.String(), "itest-gr-dev") {
		t.Fatalf("fleet filter: %s", w.Body.String())
	}
	if w := call(mux, "itest-gr", "viewer", "GET", "/v1/devices?group_id=other", ""); strings.Contains(w.Body.String(), "itest-gr-dev") {
		t.Fatalf("filter by another group still returned the device: %s", w.Body.String())
	}
	if w := call(mux, "itest-gr", "viewer", "GET", "/v1/groups", ""); !strings.Contains(w.Body.String(), `"devices":1`) {
		t.Fatalf("list: %s", w.Body.String())
	}

	// attribute definitions: admin only, then enforced on device attributes
	if call(mux, "itest-gr", "operator", "PUT", "/v1/attribute-defs/zone", `{"type":"enum","enum_values":["A","B"]}`).Code != 403 {
		t.Fatal("operator defined an attribute")
	}
	for _, c := range []string{`{"type":"enum"}`, `{"type":"float"}`} {
		if call(mux, "itest-gr", "admin", "PUT", "/v1/attribute-defs/zone", c).Code != 400 {
			t.Fatalf("accepted %s", c)
		}
	}
	for k, c := range map[string]string{"zone": `{"type":"enum","enum_values":["A","B"]}`, "serial": `{"type":"string","required":true}`} {
		if call(mux, "itest-gr", "admin", "PUT", "/v1/attribute-defs/"+k, c).Code != 200 {
			t.Fatalf("define %s", k)
		}
	}
	put := func(tenant, body string) int {
		return call(mux, tenant, "operator", "PUT", "/v1/devices/"+tenant+"-dev/attributes", body).Code
	}
	if put("itest-gr", `{"attributes":{"zone":"A"}}`) != 400 {
		t.Fatal("a missing required attribute was accepted")
	}
	if put("itest-gr", `{"attributes":{"serial":"S1","zone":"Z"}}`) != 400 {
		t.Fatal("a bad enum value was accepted")
	}
	if put("itest-gr", `{"attributes":{"serial":"S1","zone":"B","note":"free"}}`) != 200 {
		t.Fatal("a valid set was refused")
	}
	if put("itest-gr2", `{"attributes":{"zone":"Z"}}`) != 200 {
		t.Fatal("another tenant's attributes must not see these definitions")
	}
	if call(mux, "itest-gr", "admin", "DELETE", "/v1/attribute-defs/serial", "").Code != 204 || put("itest-gr", `{"attributes":{"zone":"A"}}`) != 200 {
		t.Fatal("deleting a definition should lift the rule")
	}
	if call(mux, "itest-gr", "operator", "DELETE", "/v1/groups/"+g.ID, "").Code != 204 {
		t.Fatal("delete group")
	}
}
