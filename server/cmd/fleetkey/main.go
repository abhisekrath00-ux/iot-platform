// Command fleetkey prints a new Ed25519 key pair for signing fleet release manifests.
// Put the seed in the API's FLEET_SIGNING_KEY and the public key in each edge's fleet_public_key.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("FLEET_SIGNING_KEY=" + base64.StdEncoding.EncodeToString(priv.Seed()))
	fmt.Println("fleet_public_key: " + base64.StdEncoding.EncodeToString(pub))
}
