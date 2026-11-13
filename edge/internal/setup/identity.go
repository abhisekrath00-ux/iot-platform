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
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "identity.crt"), filepath.Join(dir, "identity.key"))
	if err != nil {
		return fmt.Errorf("this identity has no usable client certificate and key; secure pairing needs administrator help, not a replacement claim")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.Subject.CommonName != serial {
		return fmt.Errorf("saved client certificate does not match this gateway serial")
	}
	ca, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		return fmt.Errorf("saved broker CA certificate is missing")
	}
	deviceCA, e := os.ReadFile(filepath.Join(dir, "device-ca.pem"))
	if e == nil {
		ca = deviceCA
	} else if !os.IsNotExist(e) {
		return fmt.Errorf("saved device issuing CA unreadable")
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
