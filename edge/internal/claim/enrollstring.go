package claim

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// An enroll string packs the control-plane URL, claim code and gateway serial
// into one value the installer pastes (or scans as a QR): the edge agent then
// needs a single argument. It carries the one-time code, so treat it as a
// secret until used. Format: hexmon-enroll:1:<base64url(json)>.
const enrollPrefix = "hexmon-enroll:1:"

type Enroll struct {
	API    string `json:"api"`
	Code   string `json:"code"`
	Serial string `json:"serial"`
	// Fallbacks are tried in order when API cannot be reached (other networks, NAT, HA).
	Fallbacks []string `json:"fb,omitempty"`
}

func EncodeEnroll(e Enroll) string {
	b, _ := json.Marshal(e)
	return enrollPrefix + base64.RawURLEncoding.EncodeToString(b)
}

func ParseEnroll(s string) (Enroll, error) {
	var e Enroll
	rest, ok := strings.CutPrefix(strings.TrimSpace(s), enrollPrefix)
	if !ok {
		return e, fmt.Errorf("not a hexmon enroll string")
	}
	b, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil || json.Unmarshal(b, &e) != nil {
		return Enroll{}, fmt.Errorf("enroll string is damaged")
	}
	u, err := url.Parse(e.API)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || e.Code == "" || e.Serial == "" {
		return Enroll{}, fmt.Errorf("enroll string is missing the server URL, code or serial")
	}
	for _, f := range e.Fallbacks {
		fu, err := url.Parse(f)
		if err != nil || (fu.Scheme != "https" && fu.Scheme != "http") || fu.Host == "" || fu.User != nil {
			return Enroll{}, fmt.Errorf("enroll string has a bad fallback address")
		}
	}
	return e, nil
}

// URLs returns the primary address followed by the fallbacks.
func (e Enroll) URLs() []string { return append([]string{e.API}, e.Fallbacks...) }
