package collector

import (
	"math"
	"time"
)

// deltaRate returns how fast a monotonic counter grew between prev and cur, in
// counter units per second. ok is false when the counter moved backwards (the
// kernel or device reset it) or when dt is not positive.
func deltaRate(prev, cur uint64, dt time.Duration) (float64, bool) {
	if dt <= 0 || cur < prev {
		return 0, false
	}

	return float64(cur-prev) / dt.Seconds(), true
}

// clampPercent limits v to the contract-01 sensor range.
func clampPercent(v float64) float64 {
	return math.Min(math.Max(v, 0), 100)
}
