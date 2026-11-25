package claim

// Read-only adapter used by the actual agent command. It has no HTTP client,
// claim code field, enrollment operation or installer mutation capability.
import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
)

func RunCandidateReview(input io.Reader, output io.Writer) error {
	raw, e := io.ReadAll(io.LimitReader(input, 131073))
	if e != nil || len(raw) > 131072 {
		return fmt.Errorf("bounded review input required")
	}
	var v struct {
		Dir, Server, Serial, DeviceCA, ExpectedTenant string
		ResponseKey                                   ed25519.PublicKey
	}
	if candidateDecodeRecovery(raw, &v) != nil {
		return fmt.Errorf("invalid read-only review input")
	}
	if v.Dir == "" || v.Server == "" || v.Serial == "" || v.ExpectedTenant == "" || len(v.ResponseKey) != ed25519.PublicKeySize {
		return fmt.Errorf("explicit retained target and trust required")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(v.DeviceCA)) {
		return fmt.Errorf("separate device trust required")
	}
	out, e := ReviewCandidateAttempt(v.Dir, v.Server, v.Serial, v.ExpectedTenant, roots, v.ResponseKey, []byte(v.DeviceCA))
	if e != nil {
		return fmt.Errorf("saved attempt review refused; preserve all identity files")
	}
	return json.NewEncoder(output).Encode(out)
}
