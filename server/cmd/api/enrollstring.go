package main

import (
	"encoding/base64"
	"encoding/json"
)

// enrollString mirrors edge/internal/claim EncodeEnroll: one value holding the
// control-plane URL, one-time claim code and serial, so an installer runs
// `edge-agent -enroll <value>` with nothing else to type. It contains the
// claim code, so it is shown once like the code itself.
func enrollString(api, code, serial string, fallbacks ...string) string {
	m := map[string]any{"api": api, "code": code, "serial": serial}
	if len(fallbacks) > 0 {
		m["fb"] = fallbacks
	}
	b, _ := json.Marshal(m)
	return "hexmon-enroll:1:" + base64.RawURLEncoding.EncodeToString(b)
}
