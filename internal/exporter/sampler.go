package exporter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"sync"
	"time"

	"fleetpulse/internal/model"
	"fleetpulse/internal/ringbuf"
	"fleetpulse/internal/sensor"
	"fleetpulse/internal/telemetry"
)

// collectTimeout bounds one collector call inside a tick.
const collectTimeout = 800 * time.Millisecond

// Sampler collects every sensor on each tick and keeps the last bufferSeconds
// of one-second samples. It is safe for concurrent use.
type Sampler struct {
	cols     []Collector
	interval time.Duration
	reg      *telemetry.Registry
	log      *slog.Logger

	mu        sync.Mutex
	ring      *ringbuf.Ring[model.Sample]
	available []sensor.Sensor

	warnMu sync.Mutex
	warned map[sensor.Sensor]time.Time
}

// NewSampler returns a sampler that runs cols every interval and buffers
// bufferSeconds samples. reg and log may be nil.
func NewSampler(cols []Collector, interval time.Duration, bufferSeconds int, reg *telemetry.Registry, log *slog.Logger) *Sampler {
	s := &Sampler{
		cols:     cols,
		interval: interval,
		reg:      reg,
		log:      log,
		ring:     ringbuf.New[model.Sample](bufferSeconds),
		warned:   make(map[sensor.Sensor]time.Time),
	}

	if reg != nil {
		reg.Counter("ticks")
		reg.Counter("sensor_errors")
		reg.Gauge("buffer_len")
	}

	return s
}

// Run ticks every interval, aligned to whole seconds, until ctx is cancelled.
// A tick that takes longer than interval is not queued; the next tick starts at
// the next interval boundary.
func (s *Sampler) Run(ctx context.Context) error {
	if s.interval <= 0 {
		return fmt.Errorf("sampler interval %s must be positive", s.interval)
	}

	next := time.Now().Truncate(time.Second).Add(time.Second)
	timer := time.NewTimer(time.Until(next))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			s.Tick(ctx, time.Now())

			next = next.Add(s.interval)
			now := time.Now()
			for !next.After(now) {
				next = next.Add(s.interval)
			}
			timer.Reset(time.Until(next))
		}
	}
}

// Tick runs one sampling round using now as the sample timestamp.
func (s *Sampler) Tick(ctx context.Context, now time.Time) {
	if ctx.Err() != nil {
		return
	}

	type result struct {
		value float64
		ok    bool
		err   error
	}

	results := make([]result, len(s.cols))
	var wg sync.WaitGroup
	wg.Add(len(s.cols))

	for i, col := range s.cols {
		go func(i int, col Collector) {
			defer wg.Done()

			collectCtx, cancel := context.WithTimeout(ctx, collectTimeout)
			defer cancel()

			value, ok, err := col.Collect(collectCtx)
			if err == nil && !ok && errors.Is(collectCtx.Err(), context.DeadlineExceeded) {
				err = collectCtx.Err()
			}
			results[i] = result{value: value, ok: ok, err: err}
		}(i, col)
	}
	wg.Wait()

	values := make(map[sensor.Sensor]float64, len(s.cols))
	okSensors := make(map[sensor.Sensor]bool, len(s.cols))

	for i, res := range results {
		name := s.cols[i].Sensor()

		switch {
		case res.err != nil:
			if ctx.Err() != nil {
				continue
			}
			s.sensorFailed(name, res.err, now)
		case !res.ok:
			continue
		case name.ID() == 0:
			s.sensorFailed(name, fmt.Errorf("unknown sensor %q", name), now)
		default:
			value, valid := normalize(res.value)
			if !valid {
				s.sensorFailed(name, errors.New("non-finite sensor value"), now)
				continue
			}
			values[name] = value
			okSensors[name] = true
		}
	}

	if s.reg != nil {
		s.reg.Counter("ticks").Inc()
	}

	available := make([]sensor.Sensor, 0, len(okSensors))
	for _, name := range sensor.All() {
		if okSensors[name] {
			available = append(available, name)
		}
	}

	s.mu.Lock()
	s.available = available
	if len(values) > 0 {
		s.ring.Push(model.Sample{TS: now.Unix(), Values: values})
	}
	bufferLen := s.ring.Len()
	s.mu.Unlock()

	if s.reg != nil {
		s.reg.Gauge("buffer_len").Set(int64(bufferLen))
	}
}

// Samples implements SampleSource. It returns the oldest buffered samples with
// ts > since, up to limit, and the contract-01 truncated flag.
func (s *Sampler) Samples(since int64, limit int) ([]model.Sample, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	samples := []model.Sample{}
	var oldest int64
	haveOldest := false

	s.ring.Each(func(sample model.Sample) bool {
		if !haveOldest {
			oldest = sample.TS
			haveOldest = true
		}
		if sample.TS <= since {
			return true
		}
		if len(samples) >= limit {
			return false
		}
		sample.Values = maps.Clone(sample.Values)
		samples = append(samples, sample)
		return true
	})

	truncated := since > 0 && haveOldest && oldest > since+1

	return samples, truncated
}

// Available returns the sensors that returned a value in the last tick, in
// contract-01 order.
func (s *Sampler) Available() []sensor.Sensor {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]sensor.Sensor(nil), s.available...)
}

// sensorFailed counts a failed read and logs it at most once per sensor per
// minute, measured against the tick timestamp.
func (s *Sampler) sensorFailed(name sensor.Sensor, err error, now time.Time) {
	if s.reg != nil {
		s.reg.Counter("sensor_errors").Inc()
	}

	if s.log == nil {
		return
	}

	s.warnMu.Lock()
	last, warned := s.warned[name]
	if warned && now.Sub(last) < time.Minute {
		s.warnMu.Unlock()
		return
	}
	s.warned[name] = now
	s.warnMu.Unlock()

	s.log.Warn("sensor collection failed", "sensor", name, "error", err)
}

// normalize clamps a reading to [0, 100] and rounds it to two decimals.
func normalize(value float64) (float64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}

	value = math.Min(math.Max(value, 0), 100)
	return math.Round(value*100) / 100, true
}
