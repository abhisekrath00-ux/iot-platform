CREATE TABLE IF NOT EXISTS report_drive_exports(
    id UUID PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    report_id TEXT NOT NULL,
    format TEXT NOT NULL,               -- html | sheet
    drive_file_id TEXT NOT NULL DEFAULT '',
    web_link TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,               -- exported | failed
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS report_drive_exports_report ON report_drive_exports(report_id, created_at DESC);
