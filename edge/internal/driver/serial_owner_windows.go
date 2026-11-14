//go:build windows

package driver

import (
	"fmt"
	"regexp"
	"strings"
)

var canonicalCOM = regexp.MustCompile(`^COM[1-9][0-9]{0,2}$`)

func serialIdentity(name string) (string, string, error) {
	name = strings.ToUpper(name)
	if !canonicalCOM.MatchString(name) {
		return "", "", fmt.Errorf("choose a Windows COM port, e.g. COM3")
	}
	return name, name, nil
}

// The serial library opens Windows COM ports with sharing disabled before it
// changes framing. The local reservation also blocks aliases/case variants.
func serialLease(path, key string) (func(), error) { return func() {}, nil }
