package discover

import (
	"context"
	"fmt"
	"net"
	"time"
)

// BACnetDevice is one I-Am answer to a Who-Is broadcast.
type BACnetDevice struct {
	Addr     string
	Instance uint32
	Vendor   uint32
}

// WhoIs is a global Who-Is inside BVLC Original-Broadcast-NPDU.
var WhoIs = []byte{0x81, 0x0b, 0x00, 0x0c, 0x01, 0x20, 0xff, 0xff, 0x00, 0xff, 0x10, 0x08}

// ParseIAm extracts the device instance and vendor id from a BACnet/IP I-Am.
func ParseIAm(b []byte) (BACnetDevice, error) {
	var d BACnetDevice
	if len(b) < 8 || b[0] != 0x81 || (b[1] != 0x0a && b[1] != 0x0b && b[1] != 0x04) {
		return d, fmt.Errorf("not BACnet/IP")
	}
	off := 4
	if b[1] == 0x04 { // Forwarded-NPDU carries the original 6-byte address first
		off = 10
	}
	if off+2 > len(b) || b[off] != 0x01 {
		return d, fmt.Errorf("bad NPDU")
	}
	ctl := b[off+1]
	off += 2
	if ctl&0x80 != 0 {
		return d, fmt.Errorf("network-layer message")
	}
	if ctl&0x20 != 0 { // DNET, DLEN, DADR
		if off+3 > len(b) {
			return d, fmt.Errorf("short")
		}
		off += 3 + int(b[off+2])
	}
	if ctl&0x08 != 0 { // SNET, SLEN, SADR
		if off+3 > len(b) {
			return d, fmt.Errorf("short")
		}
		off += 3 + int(b[off+2])
	}
	if ctl&0x20 != 0 {
		off++ // hop count
	}
	// APDU: unconfirmed request (0x10), service I-Am (0x00), object id (tag C4), instance, then
	// max-APDU, segmentation and vendor id.
	if off+8 > len(b) || b[off] != 0x10 || b[off+1] != 0x00 || b[off+2] != 0xC4 {
		return d, fmt.Errorf("not an I-Am")
	}
	oid := uint32(b[off+3])<<24 | uint32(b[off+4])<<16 | uint32(b[off+5])<<8 | uint32(b[off+6])
	if oid>>22 != 8 {
		return d, fmt.Errorf("object is not a device")
	}
	d.Instance = oid & 0x3FFFFF
	// optional tail: application-tagged unsigned max-APDU, enumerated segmentation, unsigned vendor
	p := off + 7
	for i := 0; i < 3 && p < len(b); i++ {
		l := int(b[p] & 0x07)
		if l > 4 || p+1+l > len(b) {
			break
		}
		if i == 2 {
			for _, x := range b[p+1 : p+1+l] {
				d.Vendor = d.Vendor<<8 | uint32(x)
			}
		}
		p += 1 + l
	}
	return d, nil
}

// FindBACnet broadcasts a Who-Is and collects I-Am answers. Broadcast is the
// subnet broadcast address of the plant network (e.g. 192.168.1.255); empty
// means 255.255.255.255. It reads nothing from the devices beyond identity.
func FindBACnet(ctx context.Context, broadcast string, timeout time.Duration) ([]BACnetDevice, error) {
	if broadcast == "" {
		broadcast = "255.255.255.255"
	}
	ip := net.ParseIP(broadcast)
	if ip == nil || ip.To4() == nil {
		return nil, fmt.Errorf("broadcast must be an IPv4 address")
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.WriteToUDP(WhoIs, &net.UDPAddr{IP: ip, Port: 47808}); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	seen := map[string]bool{}
	var out []BACnetDevice
	buf := make([]byte, 1500)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		conn.SetReadDeadline(deadline)
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		d, err := ParseIAm(buf[:n])
		if err != nil {
			continue
		}
		d.Addr = from.IP.String()
		if k := fmt.Sprint(d.Addr, d.Instance); !seen[k] {
			seen[k] = true
			out = append(out, d)
		}
	}
	return out, nil
}
