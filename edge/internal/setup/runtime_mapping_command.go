package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"io"
	"path/filepath"
)

// RunRuntimeMapping emits a review artifact on stdout. It writes no file,
// does not invoke systemd, and cannot permit service activation.
func RunRuntimeMapping(input io.Reader, output io.Writer) error {
	raw, e := io.ReadAll(io.LimitReader(input, 131073))
	if e != nil || len(raw) > 131072 {
		return fmt.Errorf("bounded mapping input required")
	}
	var in struct{ IdentityDir, InputDir, Serial, Tenant, Binary, ConfigPath, StateDir, User string }
	if e = decodeMappingInput(raw, &in); e != nil {
		return e
	}
	identity, e := claim.ReadCandidatePrivateFiles(in.IdentityDir, "identity.json", "identity.key", "identity.crt", "device-ca.pem")
	if e != nil {
		return fmt.Errorf("accepted private identity unavailable")
	}
	if e = ValidateIdentityMaterial(identity["identity.crt"], identity["identity.key"], identity["device-ca.pem"], in.Serial); e != nil {
		return e
	}
	var id claim.Identity
	if json.Unmarshal(identity["identity.json"], &id) != nil || id.GatewayID == "" {
		return fmt.Errorf("accepted identity malformed")
	}
	data, e := claim.ReadCandidatePrivateFiles(in.InputDir, "server-config.yaml", "broker-ca.pem")
	if e != nil {
		return fmt.Errorf("private configuration and separate broker trust unavailable")
	}
	mapping, e := buildRuntimeMappingChecked(data["server-config.yaml"], &id, in.Serial, in.Tenant, in.IdentityDir, filepath.Join(in.InputDir, "broker-ca.pem"), string(data["broker-ca.pem"]), in.Binary, in.ConfigPath, in.StateDir, in.User)
	if e != nil {
		return e
	}
	// Config/unit are the requested review output, never an activation receipt.
	return json.NewEncoder(output).Encode(mapping)
}

func decodeMappingInput(raw []byte, target any) error {
	scan := json.NewDecoder(bytes.NewReader(raw))
	token, e := scan.Token()
	if e != nil || token != json.Delim('{') {
		return fmt.Errorf("mapping object required")
	}
	allowedRaw, _ := json.Marshal(target)
	var allowed map[string]json.RawMessage
	json.Unmarshal(allowedRaw, &allowed)
	seen := map[string]bool{}
	for scan.More() {
		token, e = scan.Token()
		name, ok := token.(string)
		if e != nil || !ok || seen[name] {
			return fmt.Errorf("duplicate mapping field")
		}
		if _, ok = allowed[name]; !ok {
			return fmt.Errorf("unknown mapping field")
		}
		seen[name] = true
		var value json.RawMessage
		if scan.Decode(&value) != nil {
			return fmt.Errorf("invalid mapping field")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(target); e != nil {
		return fmt.Errorf("invalid mapping input")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return fmt.Errorf("single mapping input required")
	}
	return nil
}
