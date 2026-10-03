package main

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
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
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	obj, ts := extractUplink(raw)
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

// extractUplink finds the decoded fields and the timestamp in the supported
// shapes: ChirpStack v4, The Things Stack v3, Actility ThingPark ("DevEUI_uplink")
// and a Sigfox backend callback (recognised by an epoch "time" plus "seqNumber"
// or "data"). ThingPark and Sigfox send numbers as strings, so numeric strings
// are accepted for those two. Raw hex payloads (payload_hex, data) are never parsed.
func extractUplink(raw map[string]any) (map[string]any, string) {
	if up, ok := raw["DevEUI_uplink"].(map[string]any); ok {
		out := map[string]any{}
		skip := map[string]bool{"DevEUI": true, "DevAddr": true, "Time": true, "payload_hex": true, "CustomerID": true, "Lrcid": true, "Lrrid": true, "Channel": true, "SubBand": true}
		for k, v := range up {
			if skip[k] || k == "payload" {
				continue
			}
			if n, ok := numeric(v); ok {
				out[k] = n
			}
		}
		if dec, ok := up["payload"].(map[string]any); ok {
			for k, v := range dec {
				out[k] = v
			}
		}
		ts, _ := up["Time"].(string)
		return out, ts
	}
	if _, hasSeq := raw["seqNumber"]; hasSeq || (raw["data"] != nil && raw["device"] != nil && raw["time"] != nil) {
		if _, isObj := raw["object"]; !isObj {
			out := map[string]any{}
			skip := map[string]bool{"device": true, "data": true, "time": true, "deviceTypeId": true, "id": true, "station": true}
			for k, v := range raw {
				if skip[k] {
					continue
				}
				if n, ok := numeric(v); ok {
					out[k] = n
				}
			}
			ts := ""
			if n, ok := numeric(raw["time"]); ok {
				if f, isF := n.(float64); isF && f > 1e9 && f < 1e11 {
					ts = time.Unix(int64(f), 0).UTC().Format(time.RFC3339)
				}
			}
			return out, ts
		}
	}
	if obj, ok := raw["object"].(map[string]any); ok {
		ts, _ := raw["time"].(string)
		return obj, ts
	}
	if up, ok := raw["uplink_message"].(map[string]any); ok {
		dec, _ := up["decoded_payload"].(map[string]any)
		ts, _ := raw["received_at"].(string)
		return dec, ts
	}
	return nil, ""
}

// numeric accepts numbers, booleans and numeric strings. NaN and infinities are refused.
func numeric(v any) (any, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case bool:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, false
		}
		return f, true
	}
	return nil, false
}
