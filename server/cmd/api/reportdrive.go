package main

// Report export to the tenant's Google Drive (drive.file scope). The export
// always renders fresh report data and uploads a NEW file - drive.file can
// only see files this app created, so no existing tenant Drive content is
// touched. Real Google calls stay gated: every path goes through the
// tenant's OAuth grant, and absent Google configuration the endpoints
// answer honestly (503 / 409) instead of pretending.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/googlews"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/report"
)

type reportExportRow struct {
	ID        string    `json:"id"`
	Format    string    `json:"format"`
	WebLink   string    `json:"web_link"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
}

// exportReportDrive handles POST /v1/reports/{id}/export/drive with
// {"format":"html"|"sheet"}. html uploads the rendered report document;
// sheet uploads the CSV render and asks Drive to convert it into a Google
// Sheet. Both create a new Drive file per export.
func (s *server) exportReportDrive(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	tenant, id := auth.Tenant(r), r.PathValue("id")
	var in struct {
		Format string `json:"format"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil && err.Error() != "EOF" {
		http.Error(w, "bad json", 400)
		return
	}
	if in.Format != "html" && in.Format != "sheet" {
		http.Error(w, `format must be "html" or "sheet"`, 400)
		return
	}
	if s.google == nil || s.secrets == nil || len(s.secrets.Key) == 0 {
		writeJSON(w, 503, map[string]any{"error": "google workspace not configured on this deployment (GOOGLE_CLIENT_ID/SECRET)"})
		return
	}
	if _, err := googlews.AccessToken(r.Context(), s.google, s.secrets, tenant); err != nil {
		http.Error(w, "connect Google Workspace with the Drive reports surface first (Settings > Integrations > Google)", 409)
		return
	}
	var name string
	var defBytes []byte
	var reportCustomer *string
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT name, definition, customer_id FROM reports WHERE id=$1 AND tenant_id=$2`, id, tenant).
		Scan(&name, &defBytes, &reportCustomer); err != nil {
		http.Error(w, "report not found", 404)
		return
	}
	var def report.Definition
	if err := json.Unmarshal(defBytes, &def); err != nil {
		dbErr(w, err)
		return
	}
	series, _, err := s.buildSeries(r.Context(), tenant, def)
	if err != nil {
		dbErr(w, err)
		return
	}
	if def, err = s.withReportContext(r.Context(), tenant, deref(reportCustomer), def); err != nil {
		dbErr(w, err)
		return
	}
	stamp := time.Now().UTC().Format("2006-01-02 15:04")
	var fileName, contentType, convert string
	var content []byte
	switch in.Format {
	case "sheet":
		fileName = fmt.Sprintf("HexThings report - %s - %s", name, stamp)
		contentType = "text/csv"
		convert = "application/vnd.google-apps.spreadsheet"
		content = []byte(report.RenderCSV(def, series))
	default:
		fileName = fmt.Sprintf("HexThings report - %s - %s.html", name, stamp)
		contentType = "text/html"
		content = []byte(report.Render(name, def, series, time.Now()))
	}
	up := googlews.NewDriveUploader(s.google, s.secrets)
	f, upErr := up.Upload(r.Context(), tenant, fileName, contentType, convert, content)
	expID := uuid.NewString()
	if errors.Is(upErr, googlews.ErrScopeUnknown) || errors.Is(upErr, googlews.ErrScopeNotGranted) {
		http.Error(w, upErr.Error(), 409)
		return
	}
	if upErr != nil {
		s.st.Pool.Exec(r.Context(),
			`INSERT INTO report_drive_exports(id,tenant_id,report_id,format,status,error) VALUES($1,$2,$3,$4,'failed',$5)`,
			expID, tenant, id, in.Format, upErr.Error())
		http.Error(w, "drive upload failed: "+upErr.Error(), 502)
		return
	}
	if _, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO report_drive_exports(id,tenant_id,report_id,format,drive_file_id,web_link,status) VALUES($1,$2,$3,$4,$5,$6,'exported')`,
		expID, tenant, id, in.Format, f.ID, f.WebLink); err != nil {
		dbErr(w, err)
		return
	}
	s.audit(r, "report.drive_exported", id, map[string]any{"format": in.Format, "drive_file_id": f.ID})
	writeJSON(w, 200, map[string]any{"export_id": expID, "web_link": f.WebLink, "format": in.Format})
}

// listReportExports handles GET /v1/reports/{id}/exports - the recent Drive
// export history shown next to the export buttons.
func (s *server) listReportExports(w http.ResponseWriter, r *http.Request) {
	tenant, id := auth.Tenant(r), r.PathValue("id")
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, format, web_link, status, error, created_at FROM report_drive_exports
		 WHERE report_id=$1 AND tenant_id=$2 ORDER BY created_at DESC LIMIT 10`, id, tenant)
	if err != nil {
		dbErr(w, err)
		return
	}
	defer rows.Close()
	out := []reportExportRow{}
	for rows.Next() {
		var e reportExportRow
		if err := rows.Scan(&e.ID, &e.Format, &e.WebLink, &e.Status, &e.Error, &e.CreatedAt); err != nil {
			dbErr(w, err)
			return
		}
		out = append(out, e)
	}
	writeJSON(w, 200, map[string]any{"exports": out})
}

// trimSheetName keeps Drive file names within Drive's 32767-char limit; the
// practical report-name length is far shorter, this only guards the join.
func trimSheetName(s string) string {
	if len(s) > 200 {
		return strings.TrimSpace(s[:200])
	}
	return s
}
