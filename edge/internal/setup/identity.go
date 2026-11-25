package setup

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

// ValidateIdentity verifies the saved mTLS material before the guided secure
// setup can call a configuration ready to import. It does not re-enroll.
func ValidateIdentity(dir, serial string) error {
	cert, e := os.ReadFile(filepath.Join(dir, "identity.crt"))
	if e != nil {
		return fmt.Errorf("saved client certificate unavailable")
	}
	key, e := os.ReadFile(filepath.Join(dir, "identity.key"))
	if e != nil {
		return fmt.Errorf("saved private key unavailable")
	}
	ca, e := os.ReadFile(filepath.Join(dir, "device-ca.pem"))
	if os.IsNotExist(e) {
		ca, e = os.ReadFile(filepath.Join(dir, "ca.pem"))
	}
	if e != nil {
		return fmt.Errorf("saved device trust unavailable")
	}
	return ValidateIdentityMaterial(cert, key, ca, serial)
}

func ValidateIdentityMaterial(cert, key, ca []byte, serial string) error {
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return fmt.Errorf("this identity has no usable client certificate and key; secure pairing needs administrator help, not a replacement claim")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.Subject.CommonName != serial {
		return fmt.Errorf("saved client certificate does not match this gateway serial")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return fmt.Errorf("saved broker CA certificate is invalid")
	}
	intermediates := x509.NewCertPool()
	for _, b := range pair.Certificate[1:] {
		c, e := x509.ParseCertificate(b)
		if e != nil {
			return fmt.Errorf("invalid client certificate chain")
		}
		intermediates.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return fmt.Errorf("saved client certificate is expired or not trusted by the saved CA")
	}
	return nil
}
