package simulate

import (
	"time"

	"fleetpulse/internal/model"
	"fleetpulse/internal/sensor"
)

// source generates samples on demand for one simulated device.
type source struct {
	fleet         *Fleet
	index         int
	bufferSeconds int
	now           func() time.Time
}

// Samples implements exporter.SampleSource. It returns whole-second samples
// with ts > since, oldest first, and applies the same truncated rule as
// exporter.Sampler: since must be positive and older than the oldest buffered
// sample minus one.
func (s *source) Samples(since int64, limit int) ([]model.Sample, bool) {
	if s.bufferSeconds <= 0 {
		return []model.Sample{}, false
	}

	now := time.Now
	if s.now != nil {
		now = s.now
	}

	newest := now().Unix()
	oldest := newest - int64(s.bufferSeconds) + 1
	truncated := since > 0 && oldest > since+1

	start := oldest
	if since >= start {
		start = since + 1
	}
	if start > newest || limit <= 0 {
		return []model.Sample{}, truncated
	}

	count := newest - start + 1
	if int64(limit) < count {
		count = int64(limit)
	}

	samples := make([]model.Sample, 0, count)
	for ts := start; ts < start+count; ts++ {
		values := s.fleet.values(s.index, ts)
		if len(values) == 0 {
			continue
		}
		samples = append(samples, model.Sample{TS: ts, Values: values})
	}

	return samples, truncated
}

// values returns every sensor the device can read at ts.
func (f *Fleet) values(i int, ts int64) map[sensor.Sensor]float64 {
	values := make(map[sensor.Sensor]float64, len(sensor.All()))
	for _, s := range sensor.All() {
		if value, ok := f.Value(i, s, ts); ok {
			values[s] = value
		}
	}
	return values
}
