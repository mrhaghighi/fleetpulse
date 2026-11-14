package exporter

import "fleetpulse/internal/model"

// SampleSource returns buffered samples newer than since, oldest first.
//
// truncated reports that samples the caller asked for have already left the
// buffer. The exporter's Sampler implements SampleSource; the simulator
// provides its own implementation.
type SampleSource interface {
	Samples(since int64, limit int) (samples []model.Sample, truncated bool)
}
