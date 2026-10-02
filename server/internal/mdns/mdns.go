// Package mdns advertises the control-plane API on the local network
// (DNS-SD service _hexmon-api._tcp) so installers can find it with
// `edge-agent -discover` instead of typing an address. Advertising is opt-in
// (MDNS_ADVERTISE=true) and discovery never enrolls anything: a claim code is
// still required, and the installer confirms the address.
package mdns

import (
	"encoding/binary"
	"net"
	"strings"
)

const Service = "_hexmon-api._tcp.local."

func name(n string) []byte {
	var b []byte
	for _, l := range strings.Split(strings.TrimSuffix(n, "."), ".") {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	return append(b, 0)
}

func rr(owner []byte, typ uint16, ttl uint32, rdata []byte) []byte {
	b := append([]byte{}, owner...)
	b = binary.BigEndian.AppendUint16(b, typ)
	b = binary.BigEndian.AppendUint16(b, 0x8001) // IN, cache-flush
	b = binary.BigEndian.AppendUint32(b, ttl)
	b = binary.BigEndian.AppendUint16(b, uint16(len(rdata)))
	return append(b, rdata...)
}

// IsQuery reports whether the packet is a query that asks for our service.
func IsQuery(p []byte) bool {
	if len(p) < 12 || p[2]&0x80 != 0 {
		return false
	}
	qd := int(binary.BigEndian.Uint16(p[4:]))
	off := 12
	for i := 0; i < qd; i++ {
		var parts []string
		for {
			if off >= len(p) {
				return false
			}
			l := int(p[off])
			if l == 0 {
				off++
				break
			}
			if l&0xC0 != 0 || off+1+l > len(p) {
				return false
			}
			parts = append(parts, string(p[off+1:off+1+l]))
			off += 1 + l
		}
		if off+4 > len(p) {
			return false
		}
		typ := binary.BigEndian.Uint16(p[off:])
		off += 4
		if strings.EqualFold(strings.Join(parts, ".")+".", Service) && (typ == 12 || typ == 255) {
			return true
		}
	}
	return false
}

// BuildResponse answers with PTR, SRV, TXT and A records for the instance.
func BuildResponse(instance, host string, port int, ips []net.IP, txt []string) []byte {
	inst := instance + "." + Service
	hostFQ := host + ".local."
	h := make([]byte, 12)
	h[2] = 0x84 // response, authoritative
	var recs [][]byte
	recs = append(recs, rr(name(Service), 12, 4500, name(inst)))
	srv := binary.BigEndian.AppendUint16(nil, 0)
	srv = binary.BigEndian.AppendUint16(srv, 0)
	srv = binary.BigEndian.AppendUint16(srv, uint16(port))
	srv = append(srv, name(hostFQ)...)
	recs = append(recs, rr(name(inst), 33, 120, srv))
	var t []byte
	for _, s := range txt {
		t = append(t, byte(len(s)))
		t = append(t, s...)
	}
	if len(t) == 0 {
		t = []byte{0}
	}
	recs = append(recs, rr(name(inst), 16, 4500, t))
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			recs = append(recs, rr(name(hostFQ), 1, 120, v4))
		}
	}
	binary.BigEndian.PutUint16(h[6:], uint16(len(recs)))
	out := h
	for _, r := range recs {
		out = append(out, r...)
	}
	return out
}

// LocalIPv4s lists the non-loopback IPv4 addresses of this host.
func LocalIPv4s() []net.IP {
	var out []net.IP
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
			out = append(out, ipn.IP)
		}
	}
	return out
}

// Advertise answers service queries on the mDNS multicast group until stop is closed.
// It returns immediately with an error if the group cannot be joined.
func Advertise(instance, host string, port int, txt []string, stop <-chan struct{}) error {
	conn, err := net.ListenMulticastUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353})
	if err != nil {
		return err
	}
	go func() { <-stop; conn.Close() }()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if !IsQuery(buf[:n]) {
				continue
			}
			resp := BuildResponse(instance, host, port, LocalIPv4s(), txt)
			conn.WriteToUDP(resp, from)                                                   // unicast reply to the asker
			conn.WriteToUDP(resp, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}) // and the group
		}
	}()
	return nil
}
