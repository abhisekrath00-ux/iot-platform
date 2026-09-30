//go:build windows

package main

import (
	"context"
	"os"
	"os/signal"

	"golang.org/x/sys/windows/svc"
)

type svcHandler struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (h *svcHandler) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	s <- svc.Status{State: svc.StartPending}
	s <- svc.Status{State: svc.Running, Accepts: accepts}
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			s <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			s <- svc.Status{State: svc.StopPending}
			h.cancel()
			<-h.done // wait for main to flush the queue and close
			return false, 0
		}
	}
	return false, 0
}

// rootContext returns a context cancelled on Ctrl-C, or, when started by the
// Windows Service Control Manager, on service stop. The returned func must be
// called when shutdown is complete so the SCM sees a clean stop.
func rootContext() (context.Context, context.CancelFunc, func()) {
	if isSvc, err := svc.IsWindowsService(); err == nil && isSvc {
		ctx, cancel := context.WithCancel(context.Background())
		h := &svcHandler{cancel: cancel, done: make(chan struct{})}
		go func() { _ = svc.Run("HexmonEdge", h) }()
		return ctx, cancel, func() { close(h.done) }
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	return ctx, stop, func() {}
}
