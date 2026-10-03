package driver

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

var lwm2mPath = regexp.MustCompile(`^/[0-9]{1,5}/[0-9]{1,5}/[0-9]{1,5}$`)

// newLwM2M reads single numeric resources from a LwM2M client (OMA LwM2M 1.0, e.g. /3303/0/5700 is the
// temperature sensor value) as a plain CoAP GET with Accept text/plain. This is the edge acting as a
// reader, with the device addressed directly. It is NOT a LwM2M server: no bootstrap, no registration
// (the device does not register with us), no Observe, no writes or Execute, no DTLS, no TLV or JSON
// formats. Point `key` is /object/instance/resource. Tested only against an in-process simulator.
func newLwM2M(d config.Device) (Driver, error) {
	if d.Host == "" {
		return nil, fmt.Errorf("device %s: host required for lwm2m", d.ID)
	}
	if len(d.Points) == 0 {
		return nil, fmt.Errorf("device %s: no points", d.ID)
	}
	for _, p := range d.Points {
		if !lwm2mPath.MatchString(p.Key) {
			return nil, fmt.Errorf("device %s point %s: key must be /object/instance/resource such as /3303/0/5700", d.ID, p.ID)
		}
	}
	port := d.NetPort
	if port == 0 {
		port = 5683
	}
	return &coapDriver{dev: d, addr: net.JoinHostPort(d.Host, strconv.Itoa(port)), mid: uint16(time.Now().UnixNano()), lwm2m: true}, nil
}
