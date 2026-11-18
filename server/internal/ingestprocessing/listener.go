package ingestprocessing

import (
	"net"
	"sync"
)

const admissionConnectionLimit = 64
const admissionHandlerLimit = 16

// boundedAdmissionListener acquires capacity BEFORE accepting a socket, so slow
// TLS handshakes count too. It does not spawn one waiter per peer or read bytes
// before TLS. Excess peers remain subject to the operating system's listen
// backlog, not an application-level authenticated refusal or rate guarantee.
// Close wakes a capacity waiter as well as closing the underlying listener.
type boundedAdmissionListener struct {
	net.Listener
	slots chan struct{}
	done  chan struct{}
	once  sync.Once
}

func newBoundedAdmissionListener(ln net.Listener) *boundedAdmissionListener {
	return &boundedAdmissionListener{Listener: ln, slots: make(chan struct{}, admissionConnectionLimit), done: make(chan struct{})}
}
func (l *boundedAdmissionListener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	case l.slots <- struct{}{}:
	}
	conn, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	select {
	case <-l.done:
		conn.Close()
		<-l.slots
		return nil, net.ErrClosed
	default:
	}
	return &boundedAdmissionConn{Conn: conn, release: func() { <-l.slots }}, nil
}
func (l *boundedAdmissionListener) Close() error {
	var err error
	l.once.Do(func() { close(l.done); err = l.Listener.Close() })
	return err
}

type boundedAdmissionConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *boundedAdmissionConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
