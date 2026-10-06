package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/google/uuid"
)

// Server addresses: the URLs edge agents use to reach this platform. Configured
// per workspace or per site, ordered by priority; the first is the primary and
// the rest are fallbacks. Nothing here is derived from the browser address.

type endpointRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	SiteID   string `json:"site_id"`
	Priority int    `json:"priority"`
	Loopback bool   `json:"loopback"`
}

// normalizeEndpointURL accepts http(s)://host[:port] and returns it without a trailing slash.
func normalizeEndpointURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("address must look like https://host:port")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("address must be just scheme, host and optional port")
	}
	if p := u.Port(); p != "" {
		var n int
		if _, err := fmt.Sscanf(p, "%d", &n); err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("port must be 1-65535")
		}
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

// isLoopbackURL is true for localhost, 127.x, ::1 and 0.0.0.0: unreachable from any other machine.
func isLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// probeBlocked stops the reachability check being used against link-local / metadata addresses.
func probeBlocked(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified())
}

// suggestAddresses lists addresses other machines could plausibly use, from this host's interfaces.
func suggestAddresses(port string) []string {
	var out []string
	if p := strings.TrimSpace(envOr("API_PUBLIC_URL", "")); p != "" {
		if n, err := normalizeEndpointURL(p); err == nil {
			out = append(out, n)
		}
	}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLinkLocalUnicast() {
				out = append(out, "http://"+net.JoinHostPort(ipn.IP.String(), port))
			}
		}
	}
	sort.Strings(out)
	seen := map[string]bool{}
	uniq := out[:0]
	for _, v := range out {
		if !seen[v] {
			seen[v] = true
			uniq = append(uniq, v)
		}
	}
	if uniq == nil {
		uniq = []string{}
	}
	return uniq
}

// endpointsFor returns the ordered addresses for a site: site-specific first, then workspace-wide,
// then API_PUBLIC_URL. source says where the primary came from.
func (s *server) endpointsFor(ctx context.Context, tenant, site string) (urls []string, source string) {
	rows, err := s.st.Pool.Query(ctx, `SELECT url, site_id FROM server_endpoints WHERE tenant_id=$1 AND (site_id=$2 OR site_id='')
		ORDER BY (site_id='') , priority, created_at`, tenant, site)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var u, sid string
			if rows.Scan(&u, &sid) == nil {
				if source == "" {
					source = "workspace setting"
					if sid != "" {
						source = "site setting"
					}
				}
				urls = append(urls, u)
			}
		}
	}
	if env := strings.TrimSpace(envOr("API_PUBLIC_URL", "")); env != "" {
		if n, e := normalizeEndpointURL(env); e == nil {
			if source == "" {
				source = "API_PUBLIC_URL"
			}
			urls = append(urls, n)
		}
	}
	if len(urls) == 0 {
		return nil, "none"
	}
	return urls, source
}

// GET /v1/system/endpoints?site= (admin): configured addresses, the resolved list for a site, suggestions.
func (s *server) listEndpoints(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	tenant, site := auth.Tenant(r), r.URL.Query().Get("site")
	rows, err := s.st.Pool.Query(r.Context(), `SELECT id, name, url, site_id, priority FROM server_endpoints WHERE tenant_id=$1 ORDER BY site_id, priority, created_at`, tenant)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	list := []endpointRow{}
	for rows.Next() {
		var e endpointRow
		if rows.Scan(&e.ID, &e.Name, &e.URL, &e.SiteID, &e.Priority) == nil {
			e.Loopback = isLoopbackURL(e.URL)
			list = append(list, e)
		}
	}
	res, src := s.endpointsFor(r.Context(), tenant, site)
	warn := []string{}
	if len(res) == 0 {
		warn = append(warn, "No server address is configured. Edge installs cannot be given a reachable address until you add one.")
	} else if isLoopbackURL(res[0]) {
		warn = append(warn, "The primary address is localhost or loopback. Other machines cannot reach it.")
	}
	writeJSON(w, 200, map[string]any{"endpoints": list, "resolved": nonNil(res), "resolved_source": src, "suggestions": suggestAddresses(envOr("API_PORT", "8000")), "warnings": warn})
}

// POST /v1/system/endpoints {name,url,site_id,priority} (admin)
func (s *server) addEndpoint(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	var in struct {
		Name     string `json:"name"`
		URL      string `json:"url"`
		SiteID   string `json:"site_id"`
		Priority int    `json:"priority"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	u, err := normalizeEndpointURL(in.URL)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		http.Error(w, "name required (max 80 characters)", 400)
		return
	}
	if in.Priority <= 0 || in.Priority > 1000 {
		in.Priority = 100
	}
	tenant := auth.Tenant(r)
	if in.SiteID != "" {
		var n int
		s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM sites WHERE id=$1 AND tenant_id=$2`, in.SiteID, tenant).Scan(&n)
		if n == 0 {
			http.Error(w, "unknown site", 400)
			return
		}
	}
	id := uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO server_endpoints(tenant_id,id,name,url,site_id,priority,created_by) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT DO NOTHING`, tenant, id, in.Name, u, in.SiteID, in.Priority, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "server.endpoint.add", id, map[string]any{"url": u, "site_id": in.SiteID})
	writeJSON(w, 201, map[string]any{"id": id, "url": u, "loopback": isLoopbackURL(u)})
}

// DELETE /v1/system/endpoints/{id} (admin)
func (s *server) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `DELETE FROM server_endpoints WHERE tenant_id=$1 AND id=$2`, auth.Tenant(r), r.PathValue("id"))
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "server.endpoint.delete", r.PathValue("id"), nil)
	w.WriteHeader(204)
}

// POST /v1/system/endpoints/check {url} (admin): asks the address's /healthz FROM THIS SERVER.
// That proves this server can reach it; it does not prove an edge network can.
func (s *server) checkEndpoint(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	var in struct {
		URL string `json:"url"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	u, err := normalizeEndpointURL(in.URL)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	res := map[string]any{"url": u, "loopback": isLoopbackURL(u),
		"scope": "checked from the server itself; it does not prove an edge network can reach this address"}
	if probeBlocked(u) {
		res["ok"], res["error"] = false, "link-local and unspecified addresses are not checked"
		writeJSON(w, 200, res)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", u+"/healthz", nil)
	t0 := time.Now()
	resp, err := (&http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		res["ok"], res["error"] = false, "no answer: "+obsRedact(err.Error())
	} else {
		resp.Body.Close()
		res["ok"], res["status"], res["ms"] = resp.StatusCode == 200, resp.StatusCode, time.Since(t0).Milliseconds()
		if resp.StatusCode != 200 {
			res["error"] = fmt.Sprintf("answered HTTP %d on /healthz", resp.StatusCode)
		}
	}
	writeJSON(w, 200, res)
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
