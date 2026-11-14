// Command scraper pulls samples from exporter targets and publishes them to NATS.
package main

import (
	"fmt"

	"fleetpulse/internal/version"
)

func main() {
	fmt.Println("scraper", version.Version)
}
