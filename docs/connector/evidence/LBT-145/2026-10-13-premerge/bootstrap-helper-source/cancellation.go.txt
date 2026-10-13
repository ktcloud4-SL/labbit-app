package main

import (
	"context"
	"net"
)

// A caller owns the SSH client; its parent deadline also bounds all sessions.
func closeOnCancellation(ctx context.Context, connection net.Conn) {
	context.AfterFunc(ctx, func() { _ = connection.Close() })
}
