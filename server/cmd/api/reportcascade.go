package main

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Cascading report parameters: site, then asset (which must lie in that site), then device
// (which must lie in both). Each level narrows the next; a choice that does not fit the level
// above is rejected rather than silently ignored. The result feeds the existing multivalue
// "device" parameter, so its 10-device cap and validation still apply.

type cascadeDevice struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	SiteID  string  `json:"site_id"`
	AssetID *string `json:"asset_id"`
}

// cascadeDevices lists the tenant's devices with the site (via the gateway) and asset each belongs to.
func (s *server) cascadeDevices(ctx context.Context, tenant string) ([]cascadeDevice, error) {
	rows, err := s.st.Pool.Query(ctx, `SELECT d.id, d.name, COALESCE(g.site_id,''), d.asset_id
		FROM devices d LEFT JOIN gateways g ON g.id=d.gateway_id AND g.tenant_id=d.tenant_id
		WHERE d.tenant_id=$1 ORDER BY d.id LIMIT 5000`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cascadeDevice
	for rows.Next() {
		var d cascadeDevice
		if err := rows.Scan(&d.ID, &d.Name, &d.SiteID, &d.AssetID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

var errCascade = errors.New("cascade")

// resolveCascade returns the devices matching site, asset and device (each optional, device may be a
// comma list). Empty inputs return nil, nil: no narrowing requested.
func resolveCascade(devs []cascadeDevice, site, asset, device string) ([]string, error) {
	if site == "" && asset == "" {
		return nil, nil
	}
	known := func(f func(cascadeDevice) bool) bool {
		for _, d := range devs {
			if f(d) {
				return true
			}
		}
		return false
	}
	if site != "" && !known(func(d cascadeDevice) bool { return d.SiteID == site }) {
		return nil, errors.New("no devices in that site")
	}
	if asset != "" && !known(func(d cascadeDevice) bool { return d.AssetID != nil && *d.AssetID == asset }) {
		return nil, errors.New("no devices in that asset")
	}
	var match []string
	for _, d := range devs {
		if site != "" && d.SiteID != site {
			continue
		}
		if asset != "" && (d.AssetID == nil || *d.AssetID != asset) {
			continue
		}
		match = append(match, d.ID)
	}
	if len(match) == 0 {
		return nil, errors.New("the selected asset has no devices in the selected site")
	}
	if device != "" {
		in := map[string]bool{}
		for _, id := range match {
			in[id] = true
		}
		for _, id := range strings.Split(device, ",") {
			if !in[id] {
				return nil, errors.New("device is not in the selected site/asset")
			}
		}
		return strings.Split(device, ","), nil
	}
	sort.Strings(match)
	return match, nil
}

// reportOptions feeds the cascading pickers: sites, assets with the sites they appear in, devices
// with their site and asset. Tenant-scoped; customer-scoped users cannot change parameters, so
// they get nothing.
func (s *server) reportOptions(w http.ResponseWriter, r *http.Request) {
	if scopeOf(r) != "" {
		http.Error(w, "customer-scoped users cannot change report parameters", 403)
		return
	}
	tenant := auth.Tenant(r)
	devs, err := s.cascadeDevices(r.Context(), tenant)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	names := func(q string) map[string]string {
		m := map[string]string{}
		rows, err := s.st.Pool.Query(r.Context(), q, tenant)
		if err != nil {
			return m
		}
		defer rows.Close()
		for rows.Next() {
			var id, n string
			if rows.Scan(&id, &n) == nil {
				m[id] = n
			}
		}
		return m
	}
	siteNames := names(`SELECT id, name FROM sites WHERE tenant_id=$1`)
	assetNames := names(`SELECT id, name FROM assets WHERE tenant_id=$1`)
	siteSeen := map[string]bool{}
	assetSites := map[string]map[string]bool{}
	for _, d := range devs {
		if d.SiteID != "" {
			siteSeen[d.SiteID] = true
		}
		if d.AssetID != nil {
			if assetSites[*d.AssetID] == nil {
				assetSites[*d.AssetID] = map[string]bool{}
			}
			assetSites[*d.AssetID][d.SiteID] = true
		}
	}
	sites := []map[string]string{}
	for id := range siteSeen {
		sites = append(sites, map[string]string{"id": id, "name": siteNames[id]})
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i]["name"] < sites[j]["name"] })
	assets := []map[string]any{}
	for id, ss := range assetSites {
		ids := []string{}
		for sid := range ss {
			ids = append(ids, sid)
		}
		sort.Strings(ids)
		assets = append(assets, map[string]any{"id": id, "name": assetNames[id], "site_ids": ids})
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i]["name"].(string) < assets[j]["name"].(string) })
	writeJSON(w, 200, map[string]any{"sites": sites, "assets": assets, "devices": devs})
}
