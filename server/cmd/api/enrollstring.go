package main

import (
	"encoding/base64"
	"encoding/json"
)

// enrollString mirrors edge/internal/claim EncodeEnroll: one value holding the
// control-plane URL, one-time claim code and serial, so an installer runs
// `edge-agent -enroll <value>` with nothing else to type. It contains the
// claim code, so it is shown once like the code itself.
func enrollString(api, code, serial string) string {
	b, _ := json.Marshal(map[string]string{"api": api, "code": code, "serial": serial})
	return "hexmon-enroll:1:" + base64.RawURLEncoding.EncodeToString(b)
}
