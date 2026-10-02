package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

// lorawanUplink accepts the HTTP integration webhooks of ChirpStack (v4) and
// The Things Stack (v3) and stores the numeric fields of the decoded payload.
// Auth is a per-device token (see devicetokens.go): configure it as the
// Authorization header of the integration, one token per LoRaWAN device. The
// network server must do the payload decoding (codec/decoder); raw frames are not parsed here.
//
//	ChirpStack: {"time":"...","deviceInfo":{...},"object":{"temp":21.5,"nested":{"a":1}}}
//	TTS:        {"received_at":"...","uplink_message":{"decoded_payload":{"temp":21.5}}}
//
// Nested objects flatten with "_" (nested_a). Booleans become 0/1; strings and
// arrays are ignored. Fields that are not registered points are rejected in the
// response but do not fail the request.
func (s *server) lorawanUplink(w http.ResponseWriter, r *http.Request) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		http.Error(w, "device token required", http.StatusUnauthorized)
		return
	}
	tenant, device, ok := s.resolveDeviceToken(r.Context(), tok)
	if !ok {
		http.Error(w, "invalid, expired or revoked token", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var in struct {
		Time       string         `json:"time"`
		Object     map[string]any `json:"object"`
		ReceivedAt string         `json:"received_at"`
		Uplink     struct {
			Decoded map[string]any `json:"decoded_payload"`
		} `json:"uplink_message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	obj, ts := in.Object, in.Time
	if obj == nil {
		obj, ts = in.Uplink.Decoded, in.ReceivedAt
	}
	if len(obj) == 0 {
		http.Error(w, "no decoded payload: enable a payload decoder on the network server", 422)
		return
	}
	flat := map[string]float64{}
	flattenNumeric("", obj, flat, 0)
	if len(flat) == 0 {
		http.Error(w, "decoded payload has no numeric fields", 422)
		return
	}
	names := make([]string, 0, len(flat))
	for k := range flat {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) > maxIngestBatch {
		names = names[:maxIngestBatch]
	}
	readings := make([]ingestReading, 0, len(names))
	for _, k := range names {
		v := flat[k]
		readings = append(readings, ingestReading{Point: k, Value: &v, TS: normalizeTS(ts)})
	}
	s.ingestBatch(w, r, tenant, device, readings)
}

func flattenNumeric(prefix string, m map[string]any, out map[string]float64, depth int) {
	if depth > 4 {
		return
	}
	for k, v := range m {
		name := k
		if prefix != "" {
			name = prefix + "_" + k
		}
		switch x := v.(type) {
		case float64:
			out[name] = x
		case bool:
			if x {
				out[name] = 1
			} else {
				out[name] = 0
			}
		case map[string]any:
			flattenNumeric(name, x, out, depth+1)
		}
	}
}

// normalizeTS returns an RFC3339 timestamp or "" (ingest then uses now). Network
// servers send RFC3339 with nanoseconds, which time.RFC3339 parsing accepts.
func normalizeTS(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
