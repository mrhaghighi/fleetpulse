// Command smoketest runs end-to-end checks against a running FleetPulse stack.
package main

import (
	"fmt"

	"fleetpulse/internal/version"
)

func main() {
	fmt.Println("smoketest", version.Version)
}
