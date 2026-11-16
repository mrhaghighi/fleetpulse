# 0008 — Deterministic stateless simulator

## Status

Accepted

## Context

We need many virtual devices to load-test the stack, to seed a demo with history
and to make smoke tests reproducible. Real recorded traces would give realistic
shapes but only at the scale that was recorded, and a stateful generator that
advances per device cannot produce the same value for a timestamp twice.

## Decision

Simulated values are a pure function of `(seed, device, sensor, ts)`. There is
no per-device goroutine and no stored signal state. A hash of those coordinates
(FNV-1a mixing finished with splitmix64, with a separate salt per sensor
parameter) drives a base level, amplitude, phase, noise and occasional bursts.
Slow parameters change on 30-second value-noise buckets; a fanless device simply
omits `fan_speed`.

The same `Fleet.Value` function serves live simulation, backfill and smoke tests,
so a point generated today is identical to the point the same coordinates would
have produced last week.

## Alternatives considered

- **Per-device goroutines with internal state.** Natural to write and easy to
  make smooth, but memory grows with fleet size, and the value at a past
  timestamp cannot be recomputed without replaying every step.
- **Replaying recorded traces.** Realistic, but the maximum fleet size is the
  size of the recording, and there is no knob for load.
- **Random values per call.** Cheap, but a reload of the same chart shows a
  different line, and tests cannot assert anything.

## Consequences

One process simulates 10,000 devices because nothing is stored per device
beyond the device index. Backfill and smoke tests reuse the generator instead of
maintaining their own fixtures.

The signals are plausible, not real: they are smooth, bounded and bursty, but
they do not reproduce a specific kernel's CPU accounting or a particular disk.
That is acceptable for load and demo data, and it is why the UI labels simulated
devices.

Changing the hash or a salt changes every historical value. That is a breaking
change for stored history, so the algorithm is treated as part of the data
format, not as an implementation detail to tune casually.
