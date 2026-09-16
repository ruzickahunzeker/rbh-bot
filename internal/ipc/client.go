package ipc

import (
	"context"
	"net"
	"net/http"
	"time"
)

func NewUnixClient(socketPath string, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		},
		DisableKeepAlives: false,
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}
