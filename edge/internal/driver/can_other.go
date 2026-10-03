//go:build !linux

package driver

import "fmt"

func openSocketCAN(iface string) (canSource, error) {
	return nil, fmt.Errorf("CAN needs Linux SocketCAN; not available on this platform")
}
