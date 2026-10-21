package googlews

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

// DriveUploader uploads platform-generated report artifacts into the tenant's
// Drive (drive.file scope: only files this app created). The endpoint is
// injectable so tests run against a local simulator; nothing here dials
// Google by itself.
type DriveUploader struct {
	p *Provider
	s SecretStore
}

// NewDriveUploader uploads through the provider's DriveURL (the real Google
// endpoint by default, a simulator in tests).
func NewDriveUploader(p *Provider, s SecretStore) *DriveUploader {
	return &DriveUploader{p: p, s: s}
}

// DriveFile is the uploaded artifact handle returned by Drive.
type DriveFile struct {
	ID      string `json:"id"`
	WebLink string `json:"webViewLink"`
}

// Upload creates one file in the tenant's Drive from content the platform
// generated. convertMime, when non-empty, asks Drive to convert the content
// into that Google-native type (CSV -> Sheets); otherwise the bytes are
// stored as-is. HexThings only ever creates new report files through this
// uploader - nothing in the platform reads, lists, modifies or deletes
// existing Drive content, and the upload refuses to run unless the tenant's
// recorded grant covers drive.file.
func (d *DriveUploader) Upload(ctx context.Context, tenant, name, contentType, convertMime string, content []byte) (*DriveFile, error) {
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n") {
		return nil, fmt.Errorf("googlews drive: bad file name %q", name)
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("googlews drive: empty content")
	}
	if len(content) > 10<<20 {
		return nil, fmt.Errorf("googlews drive: content over 10 MiB limit")
	}
	if err := RequireScope(ctx, d.s, tenant, ScopeDriveReportFile); err != nil {
		return nil, err
	}
	at, err := AccessToken(ctx, d.p, d.s, tenant)
	if err != nil {
		return nil, err
	}
	meta := map[string]string{"name": name}
	if convertMime != "" {
		meta["mimeType"] = convertMime
	}
	metaJSON, _ := json.Marshal(meta)
	var body strings.Builder
	w := multipart.NewWriter(&body)
	mh := textproto.MIMEHeader{}
	mh.Set("Content-Type", "application/json; charset=UTF-8")
	mp, err := w.CreatePart(mh)
	if err != nil {
		return nil, err
	}
	mp.Write(metaJSON)
	ch := textproto.MIMEHeader{}
	ch.Set("Content-Type", contentType)
	cp, err := w.CreatePart(ch)
	if err != nil {
		return nil, err
	}
	cp.Write(content)
	if err := w.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.p.DriveURL+"?uploadType=multipart&fields=id,webViewLink", strings.NewReader(body.String()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "multipart/related; boundary="+w.Boundary())
	req.Header.Set("Authorization", "Bearer "+at)
	resp, err := d.p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("drive upload: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		var ge struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &ge)
		return nil, fmt.Errorf("drive upload: %s (%s)", resp.Status, ge.Error.Message)
	}
	var f DriveFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("drive upload: bad response: %w", err)
	}
	if f.ID == "" {
		return nil, fmt.Errorf("drive upload: empty file id")
	}
	return &f, nil
}
