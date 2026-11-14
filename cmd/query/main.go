// Command query serves the FleetPulse HTTP API and web UI.
package main

import (
	"fmt"

	"fleetpulse/internal/version"
)

func main() {
	fmt.Println("query", version.Version)
}
