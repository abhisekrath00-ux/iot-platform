//go:build linux

package driver

import (
	"context"
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

type socketCAN struct{ fd int }

// openSocketCAN opens a raw CAN socket bound to the interface. It is receive-only: the file descriptor is
// never written to, and no send path exists in this package.
func openSocketCAN(iface string) (canSource, error) {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, fmt.Errorf("can interface %s: %w", iface, err)
	}
	fd, err := unix.Socket(unix.AF_CAN, unix.SOCK_RAW, unix.CAN_RAW)
	if err != nil {
		return nil, fmt.Errorf("open CAN socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrCAN{Ifindex: ifc.Index}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("bind %s: %w", iface, err)
	}
	tv := unix.NsecToTimeval(int64(500 * time.Millisecond))
	unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)
	return &socketCAN{fd: fd}, nil
}

func (s *socketCAN) Frames(ctx context.Context) <-chan canFrame {
	ch := make(chan canFrame, 64)
	go func() {
		defer close(ch)
		buf := make([]byte, 16) // struct can_frame: id(4) dlc(1) pad(3) data(8)
		for ctx.Err() == nil {
			n, err := unix.Read(s.fd, buf)
			if err != nil || n < 16 {
				continue // timeout or short read: check ctx and retry
			}
			id := uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24
			if id&(unix.CAN_ERR_FLAG|unix.CAN_RTR_FLAG) != 0 {
				continue // error and remote frames carry no signal data
			}
			dlc := int(buf[4])
			if dlc > 8 {
				dlc = 8
			}
			f := canFrame{ID: id & unix.CAN_EFF_MASK, Ext: id&unix.CAN_EFF_FLAG != 0, Data: append([]byte(nil), buf[8:8+dlc]...), At: time.Now()}
			if !f.Ext {
				f.ID &= unix.CAN_SFF_MASK
			}
			select {
			case ch <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

func (s *socketCAN) Close() error { return unix.Close(s.fd) }
