// Command writer consumes NATS messages and batch-inserts them into ClickHouse.
package main

import (
	"fmt"

	"fleetpulse/internal/version"
)

func main() {
	fmt.Println("writer", version.Version)
}
