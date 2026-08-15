package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// The runtime image is distroless: no shell, no curl, nothing to invoke from a
// container healthcheck. Rather than reintroduce a package manager and a shell
// purely so a probe can run — and with them a much larger patching surface on a
// service that sits on an ingest boundary — the binary probes itself.
//
//	docker-compose.yml:  test: ["CMD", "/app", "-healthcheck"]
//
// This is only for container-level healthchecks. Kubernetes and ECS talk to
// /healthz and /readyz directly over HTTP and need none of this.

const healthcheckTimeout = 3 * time.Second

// runHealthcheck probes the local liveness endpoint and reports whether it
// answered. It deliberately calls /healthz rather than /readyz: a container
// runtime restarts an unhealthy container, and a database blip must drain this
// instance from the load balancer, not have it killed.
func runHealthcheck(addr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()

	url := "http://" + localAddress(addr) + "/healthz"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build healthcheck request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck request to %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned %s", resp.Status)
	}
	return nil
}

// localAddress turns a listen address into something dialable.
//
// HTTP_ADDR is usually ":8080", meaning "every interface", which is not a
// destination. The probe runs inside the same container, so it dials loopback.
func localAddress(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// Not host:port at all; hand it back and let the dial fail with a clear
		// error rather than guessing.
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
