// Package web serves the embedded FleetPulse web UI.
package web

import _ "embed"

// Index is the built single-page UI served at the site root.
//
//go:embed index.html
var Index []byte
