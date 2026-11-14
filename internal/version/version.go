// Package version exposes the FleetPulse build version.
package version

// Version is the FleetPulse version, overridden at build time with
// -ldflags "-X fleetpulse/internal/version.Version=<version>".
var Version = "dev"
