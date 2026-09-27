#!/usr/bin/env bash
# Generates the deployment device CA (ECDSA P-256, 10y) for mTLS gateway auth.
# Store the key 0600; high-assurance deployments should move it to a KMS/HSM.
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=${1:-deploy/ca}
mkdir -p "$OUT"
cat > /tmp/gen-ca.go <<'GO'
package main

import (
	"log"
	"os"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/pki"
)

func main() {
	cert, key, err := pki.GenerateCA("Hexmon Device CA", 10)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(os.Args[1]+"/ca.crt", cert, 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(os.Args[1]+"/ca.key", key, 0o600); err != nil {
		log.Fatal(err)
	}
	log.Printf("device CA written to %s (key is 0600)", os.Args[1])
}
GO
(cd server && go run /tmp/gen-ca.go "$(cd "$OUT" && pwd)")
rm -f /tmp/gen-ca.go
echo "set MTLS_CA_CERT=$OUT/ca.crt and MTLS_CA_KEY=$OUT/ca.key in .env"
