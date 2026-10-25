// SPDX-License-Identifier: GPL-3.0-or-later

// Package ports defines shared port rules for the manager and DSH instances.
package ports

const (
	// Max is the highest valid TCP port.
	Max = 65535
	// InstanceMin is the lowest valid DSH instance port: the manager runs as an
	// unprivileged service user, so privileged ports cannot be bound.
	InstanceMin = 1024
)

// ValidInstance reports whether port is a valid DSH instance port.
func ValidInstance(port int) bool {
	return port >= InstanceMin && port <= Max
}
