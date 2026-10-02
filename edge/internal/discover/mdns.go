// Package discover finds a Hexmon server and industrial devices on the local
// network. Discovery only SUGGESTS addresses to an installer: it never enrolls
// a gateway or sends a claim code by itself, because anything on the LAN can
// answer a multicast query.
package discover

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"time"
)

// Service is the DNS-SD type the server advertises.
const Service = "_hexmon-api._tcp.local."

// Server is one advertised control plane.
type Server struct {
	Host string // SRV target
	Port int
	IPs  []string
	TXT  []string
	From string // who answered
}

func (s Server) URL() string {
	scheme := "https"
	for _, t := range s.TXT {
		if t == "scheme=http" {
			scheme = "http"
		}
	}
	h := s.Host
	if len(s.IPs) > 0 {
		h = s.IPs[0]
	}
	return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(h, fmt.Sprint(s.Port)))
}

func encodeName(name string) []byte {
	var b []byte
	for _, l := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	return append(b, 0)
}

// BuildQuery is a one-question PTR query for the service.
func BuildQuery() []byte {
	h := make([]byte, 12)
	binary.BigEndian.PutUint16(h[4:], 1) // QDCOUNT
	q := append(h, encodeName(Service)...)
	return append(q, 0, 12, 0, 1) // PTR, IN
}

// readName decodes a possibly compressed name starting at off; returns the
// name and the offset after it. Pointer loops are bounded.
func readName(b []byte, off int) (string, int, error) {
	var parts []string
	end, jumped := -1, 0
	for {
		if off >= len(b) {
			return "", 0, fmt.Errorf("name past end")
		}
		l := int(b[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			return strings.Join(parts, ".") + ".", end, nil
		case l&0xC0 == 0xC0:
			if off+1 >= len(b) || jumped > 8 {
				return "", 0, fmt.Errorf("bad compression pointer")
			}
			if end < 0 {
				end = off + 2
			}
			off = int(binary.BigEndian.Uint16(b[off:])) & 0x3FFF
			jumped++
		case l&0xC0 != 0:
			return "", 0, fmt.Errorf("bad label")
		default:
			if off+1+l > len(b) {
				return "", 0, fmt.Errorf("label past end")
			}
			parts = append(parts, string(b[off+1:off+1+l]))
			off += 1 + l
		}
	}
}

// ParseResponse extracts servers (PTR -> SRV/A/TXT) from one mDNS response.
func ParseResponse(b []byte, from string) ([]Server, error) {
	if len(b) < 12 || b[2]&0x80 == 0 {
		return nil, fmt.Errorf("not a response")
	}
	qd, an, ns, ar := int(binary.BigEndian.Uint16(b[4:])), int(binary.BigEndian.Uint16(b[6:])), int(binary.BigEndian.Uint16(b[8:])), int(binary.BigEndian.Uint16(b[10:]))
	off := 12
	for i := 0; i < qd; i++ {
		_, n, err := readName(b, off)
		if err != nil {
			return nil, err
		}
		off = n + 4
	}
	type srv struct {
		target string
		port   int
	}
	ptrs := map[string]bool{}
	srvs := map[string]srv{}
	txts := map[string][]string{}
	addrs := map[string][]string{}
	for i := 0; i < an+ns+ar; i++ {
		name, n, err := readName(b, off)
		if err != nil || n+10 > len(b) {
			return nil, fmt.Errorf("bad record")
		}
		typ, rdlen := binary.BigEndian.Uint16(b[n:]), int(binary.BigEndian.Uint16(b[n+8:]))
		rd := n + 10
		if rd+rdlen > len(b) {
			return nil, fmt.Errorf("record past end")
		}
		switch typ {
		case 12:
			if t, _, err := readName(b, rd); err == nil && strings.EqualFold(name, Service) {
				ptrs[strings.ToLower(t)] = true
			}
		case 33:
			if rdlen >= 7 {
				if t, _, err := readName(b, rd+6); err == nil {
					srvs[strings.ToLower(name)] = srv{t, int(binary.BigEndian.Uint16(b[rd+4:]))}
				}
			}
		case 16:
			for p := rd; p < rd+rdlen; {
				l := int(b[p])
				if p+1+l > rd+rdlen {
					break
				}
				txts[strings.ToLower(name)] = append(txts[strings.ToLower(name)], string(b[p+1:p+1+l]))
				p += 1 + l
			}
		case 1:
			if rdlen == 4 {
				addrs[strings.ToLower(name)] = append(addrs[strings.ToLower(name)], net.IP(b[rd:rd+4]).String())
			}
		}
		off = rd + rdlen
	}
	var out []Server
	for inst := range ptrs {
		s, ok := srvs[inst]
		if !ok || s.port <= 0 || s.port > 65535 {
			continue
		}
		out = append(out, Server{Host: strings.TrimSuffix(s.target, "."), Port: s.port, IPs: addrs[strings.ToLower(s.target)], TXT: txts[inst], From: from})
	}
	return out, nil
}

// FindServers multicasts one query and collects answers until ctx ends or
// timeout. Results are suggestions for a human to confirm.
func FindServers(ctx context.Context, timeout time.Duration) ([]Server, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.WriteToUDP(BuildQuery(), &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	var found []Server
	seen := map[string]bool{}
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		conn.SetReadDeadline(deadline)
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		ss, err := ParseResponse(buf[:n], from.IP.String())
		if err != nil {
			continue
		}
		for _, s := range ss {
			if k := s.URL(); !seen[k] {
				seen[k] = true
				found = append(found, s)
			}
		}
	}
	return found, nil
}
