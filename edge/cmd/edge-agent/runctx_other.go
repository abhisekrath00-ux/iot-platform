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
