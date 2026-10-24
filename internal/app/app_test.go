// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/LeuJasYoh/PCL-DSH-Go/internal/server"
)

func TestPortInUse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if busy, err := portInUse(port); err != nil || !busy {
		t.Fatalf("portInUse(%d) = %v, %v; want true, nil", port, busy, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// WSL2 may keep accepting connections for a few milliseconds after a
	// listener is closed (a well-known localhost-forwarding artefact), so poll
	// for the port to become free instead of asserting immediately.
	deadline := time.Now().Add(3 * time.Second)
	for {
		busy, err := portInUse(port)
		if err != nil {
			t.Fatalf("portInUse(%d) after close: %v", port, err)
		}
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("port %d still reported busy 3s after close", port)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := portInUse(0); err == nil {
		t.Fatal("portInUse(0) must fail")
	}
}

func TestBusyErrorIsConflict(t *testing.T) {
	err := busyError()
	apiErr, ok := server.AsAPIError(err)
	if !ok {
		t.Fatalf("busyError is not *server.APIError: %v", err)
	}
	if apiErr.Status != 409 || apiErr.Code != server.CodeConflictBusy {
		t.Fatalf("busyError = %d %q", apiErr.Status, apiErr.Code)
	}
	if !errors.Is(err, err) {
		t.Fatal("unreachable")
	}
}
