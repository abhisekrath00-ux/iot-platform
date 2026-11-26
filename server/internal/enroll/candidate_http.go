package enroll

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// CandidateHandler is only exposed by explicit CandidateService composition.
// Product main supplies a disabled configuration. HTTPS, request-size/unknown-
// field bounds and generic failures also apply to direct isolated fixtures.
func (c CandidateAttempts) CandidateHandler(recoverOnly bool, issue func(AttemptBinding) (CandidateIssued, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost || r.TLS == nil {
			http.Error(w, "claim attempt refused", 403)
			return
		}
		if r.Context().Err() != nil {
			http.Error(w, "claim attempt refused", 403)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 24576)
		raw, e := io.ReadAll(r.Body)
		if e != nil {
			http.Error(w, "claim attempt refused", 403)
			return
		}
		// Reject duplicate/case-aliased keys rather than accepting last-wins JSON.
		scan := json.NewDecoder(bytes.NewReader(raw))
		token, e := scan.Token()
		if e != nil || token != json.Delim('{') {
			http.Error(w, "claim attempt refused", 403)
			return
		}
		seen := map[string]bool{}
		allowed := map[string]bool{"attempt_id": true, "serial": true, "csr_pem": true, "claim_code": true, "signature": true}
		for scan.More() {
			token, e = scan.Token()
			name, ok := token.(string)
			if e != nil || !ok || !allowed[name] || seen[name] {
				http.Error(w, "claim attempt refused", 403)
				return
			}
			seen[name] = true
			var value json.RawMessage
			if scan.Decode(&value) != nil {
				http.Error(w, "claim attempt refused", 403)
				return
			}
		}
		if _, e = scan.Token(); e != nil {
			http.Error(w, "claim attempt refused", 403)
			return
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var wire struct {
			ID        string `json:"attempt_id"`
			Serial    string `json:"serial"`
			CSR       string `json:"csr_pem"`
			Code      string `json:"claim_code"`
			Signature []byte `json:"signature"`
		}
		if dec.Decode(&wire) != nil {
			http.Error(w, "claim attempt refused", 403)
			return
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			http.Error(w, "claim attempt refused", 403)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		out, e := c.Execute(ctx, AttemptInput{wire.ID, wire.Serial, wire.CSR, wire.Code, wire.Signature}, recoverOnly, issue)
		if e != nil {
			http.Error(w, "claim attempt unavailable; preserve retained identity", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(out)
	})
}
