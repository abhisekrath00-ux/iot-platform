package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

func TestResolveCascade(t *testing.T) {
	devs := []cascadeDevice{
		{"d1", "D1", "s1", ptr("a1")}, {"d2", "D2", "s2", ptr("a1")}, {"d3", "D3", "s2", ptr("a2")}, {"d4", "D4", "s2", nil},
	}
	cases := []struct {
		site, asset, device string
		want                string // comma list, "" = nil, "ERR" = error
	}{
		{"", "", "", ""},
		{"s2", "", "", "d2,d3,d4"},
		{"", "a1", "", "d1,d2"},
		{"s2", "a1", "", "d2"},
		{"s1", "a2", "", "ERR"},      // asset exists but not in that site
		{"nope", "", "", "ERR"},      // unknown site
		{"", "nope", "", "ERR"},      // unknown asset
		{"s2", "", "d3", "d3"},       // device inside the site
		{"s2", "", "d1", "ERR"},      // device outside the site
		{"s2", "a1", "d3", "ERR"},    // device outside the asset
		{"s2", "", "d2,d3", "d2,d3"}, // several
	}
	for _, c := range cases {
		got, err := resolveCascade(devs, c.site, c.asset, c.device)
		if c.want == "ERR" {
			if err == nil {
				t.Errorf("%+v: expected error, got %v", c, got)
			}
			continue
		}
		if err != nil || strings.Join(got, ",") != c.want {
			t.Errorf("%+v: got %v, %v want %q", c, got, err, c.want)
		}
	}
}

func TestIntegrationReportCascade(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-cc")
	seed(t, s, "itest-cc2")
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM assets WHERE tenant_id IN ('itest-cc','itest-cc2')`,
		`INSERT INTO assets(id,tenant_id,name) VALUES('itest-cc-a1','itest-cc','Line A'),('itest-cc-a2','itest-cc','Line B')`,
		`INSERT INTO sites(id,tenant_id,name) VALUES('itest-cc-site2','itest-cc','Annex') ON CONFLICT DO NOTHING`,
		`INSERT INTO gateways(id,tenant_id,site_id,serial,status) VALUES('itest-cc-gw2','itest-cc','itest-cc-site2','SER-cc2','active') ON CONFLICT DO NOTHING`,
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name,config) VALUES
		   ('itest-cc-d2','itest-cc','itest-cc-gw2','modbus-tcp','D2','{"connection":{"host":"10.0.0.8","address":1,"interval_seconds":5}}'),
		   ('itest-cc-d3','itest-cc','itest-cc-gw2','modbus-tcp','D3','{"connection":{"host":"10.0.0.9","address":1,"interval_seconds":5}}')`,
		`INSERT INTO points(id,device_id,unit,min_value,max_value) VALUES('temp','itest-cc-d2','C',-40,150),('temp','itest-cc-d3','C',-40,150)`,
		`INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version)
		 SELECT 'cc-'||d||g,'itest-cc','itest-cc-gw2','itest-cc-'||d,'temp', now()-(g||' minutes')::interval, 10+g,'C',1
		 FROM generate_series(1,5) g, (VALUES ('d2'),('d3')) v(d)`,
		`UPDATE devices SET asset_id='itest-cc-a1' WHERE id IN ('itest-cc-dev','itest-cc-d2')`,
		`UPDATE devices SET asset_id='itest-cc-a2' WHERE id='itest-cc-d3'`,
	} {
		if _, err := s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	w := call(h, "itest-cc", "admin", "POST", "/v1/reports", `{"name":"C","definition":{"metrics":[{"device_id":"itest-cc-dev","point_id":"temp"}],"window_hours":2,"group_by":"hour"}}`)
	var rep struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &rep)
	dl := func(tenant, q string) (int, string) {
		w := call(h, tenant, "viewer", "GET", "/v1/reports/"+rep.ID+"/download?format=csv&"+q, "")
		return w.Code, w.Body.String()
	}
	if c, b := dl("itest-cc", "site=itest-cc-site2"); c != 200 || !strings.Contains(b, "itest-cc-d2") || !strings.Contains(b, "itest-cc-d3") || strings.Contains(b, "itest-cc-dev") {
		t.Fatalf("site cascade: %d %s", c, b)
	}
	if c, b := dl("itest-cc", "site=itest-cc-site2&asset=itest-cc-a1"); c != 200 || !strings.Contains(b, "itest-cc-d2") || strings.Contains(b, "itest-cc-d3") {
		t.Fatalf("site+asset cascade: %d %s", c, b)
	}
	if c, b := dl("itest-cc", "site=itest-cc-site2&asset=itest-cc-a1&device=itest-cc-d2"); c != 200 || !strings.Contains(b, "itest-cc-d2") {
		t.Fatalf("three levels: %d %s", c, b)
	}
	for _, bad := range []string{
		"site=itest-cc-site&asset=itest-cc-a2",                     // asset not in site
		"site=itest-cc-site2&device=itest-cc-dev",                  // device outside site
		"site=itest-cc-site2&asset=itest-cc-a1&device=itest-cc-d3", // device outside asset
		"site=nope", // unknown
		"asset=itest-cc-a1%27%20OR%20%271%27%3D%271", // injection shape
	} {
		if c, b := dl("itest-cc", bad); c != 400 {
			t.Errorf("%s: %d %s, want 400", bad, c, b)
		}
	}
	// another tenant cannot cascade into this tenant's site (nothing matches there)
	if c, _ := dl("itest-cc2", "site=itest-cc-site2"); c != 404 && c != 400 {
		t.Fatalf("cross-tenant cascade = %d", c)
	}
	// options for the pickers
	w = call(h, "itest-cc", "viewer", "GET", "/v1/reports/options", "")
	var opt struct {
		Sites  []map[string]string `json:"sites"`
		Assets []struct {
			ID      string   `json:"id"`
			SiteIDs []string `json:"site_ids"`
		} `json:"assets"`
		Devices []cascadeDevice `json:"devices"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &opt) != nil || len(opt.Sites) != 2 || len(opt.Assets) != 2 || len(opt.Devices) != 3 {
		t.Fatalf("options: %d %s", w.Code, w.Body.String())
	}
	w = call(h, "itest-cc2", "viewer", "GET", "/v1/reports/options", "")
	if strings.Contains(w.Body.String(), "itest-cc-d2") || strings.Contains(w.Body.String(), "Annex") {
		t.Fatalf("options leaked across tenants: %s", w.Body.String())
	}
}
