//go:build !windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func rootContext() (context.Context, context.CancelFunc, func()) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx, stop, func() {}
}

func runningAsService() bool { return false }

// syscallNoCtty keeps opening a serial device from making it our controlling terminal.
func syscallNoCtty() int { return syscall.O_NOCTTY }
