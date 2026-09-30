package driver

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

// newModbusTCP builds the config-driven Modbus driver over TCP (PLCs, SCADA
// RTUs, serial-to-Ethernet bridges, meters with Ethernet). The connection is
// lazy and re-dialed after any I/O error so a flapping device never wedges
// the poll loop.
func newModbusTCP(d config.Device) (Driver, error) {
	if d.Host == "" {
		return nil, fmt.Errorf("device %s: host required for modbus-tcp", d.ID)
	}
	port := d.NetPort
	if port == 0 {
		port = 502
	}
	addr := net.JoinHostPort(d.Host, strconv.Itoa(port))
	t := &tcpConn{addr: addr, unit: byte(d.Address)}
	g := &modbusGeneric{dev: d}
	g.read = t.read
	g.close = t.close
	return g, nil
}

type tcpConn struct {
	addr string
	unit byte
	conn net.Conn
	tid  atomic.Uint32
}

func (t *tcpConn) close() error {
	if t.conn != nil {
		err := t.conn.Close()
		t.conn = nil
		return err
	}
	return nil
}

func (t *tcpConn) read(fn, reg, count int) ([]byte, error) {
	if t.conn == nil {
		c, err := net.DialTimeout("tcp", t.addr, 3*time.Second)
		if err != nil {
			return nil, err
		}
		t.conn = c
	}
	data, err := t.exchange(fn, reg, count)
	if err != nil {
		t.close() // force re-dial next poll
	}
	return data, err
}

func (t *tcpConn) exchange(fn, reg, count int) ([]byte, error) {
	tid := uint16(t.tid.Add(1))
	req := make([]byte, 12)
	binary.BigEndian.PutUint16(req[0:], tid)
	binary.BigEndian.PutUint16(req[2:], 0) // protocol id
	binary.BigEndian.PutUint16(req[4:], 6) // remaining length
	req[6] = t.unit
	req[7] = byte(fn)
	binary.BigEndian.PutUint16(req[8:], uint16(reg))
	binary.BigEndian.PutUint16(req[10:], uint16(count))
	t.conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := t.conn.Write(req); err != nil {
		return nil, err
	}
	hdr := make([]byte, 7)
	if _, err := io.ReadFull(t.conn, hdr); err != nil {
		return nil, err
	}
	if binary.BigEndian.Uint16(hdr[0:]) != tid {
		return nil, fmt.Errorf("transaction id mismatch")
	}
	l := int(binary.BigEndian.Uint16(hdr[4:]))
	if l < 2 || l > 260 {
		return nil, fmt.Errorf("bad MBAP length %d", l)
	}
	pdu := make([]byte, l-1)
	if _, err := io.ReadFull(t.conn, pdu); err != nil {
		return nil, err
	}
	if pdu[0] == byte(fn)|0x80 {
		return nil, fmt.Errorf("modbus exception code %d", pdu[1])
	}
	if pdu[0] != byte(fn) || len(pdu) < 2 || int(pdu[1]) != len(pdu)-2 {
		return nil, fmt.Errorf("malformed response")
	}
	return pdu[2:], nil
}
