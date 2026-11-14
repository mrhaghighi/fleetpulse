// Command backfill generates and inserts historical samples.
package main

import (
	"fmt"

	"fleetpulse/internal/version"
)

func main() {
	fmt.Println("backfill", version.Version)
}
