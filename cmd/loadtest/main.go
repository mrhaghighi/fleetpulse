// Command loadtest measures FleetPulse under increasing load.
package main

import (
	"fmt"

	"fleetpulse/internal/version"
)

func main() {
	fmt.Println("loadtest", version.Version)
}
