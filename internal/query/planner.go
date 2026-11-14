package query

import (
	"errors"
	"fmt"
)

// Tier is one storage tier from contract 04.
type Tier struct {
	// Name is the tier label reported as the response "source".
	Name string
	// Table is the ClickHouse table holding this tier.
	Table string
	// ResolutionSec is the bucket width stored in the table.
	ResolutionSec int64
	// RetentionSec is how far back the table keeps data.
	RetentionSec int64
}

// Plan is the chosen tier and bucket step for one query.
type Plan struct {
	Tier    Tier
	StepSec int64
}

const (
	rawRetentionSec     = 7 * 24 * 3600
	oneMinRetentionSec  = 90 * 24 * 3600
	oneHourRetentionSec = 400 * 24 * 3600

	minWindowSec     = 60
	maxWindowSec     = oneHourRetentionSec
	minMaxPoints     = 100
	maxMaxPoints     = 2000
	defaultMaxPoints = 1000
)

// tiers is ordered finest to coarsest; planQuery walks it backwards to find
// the coarsest tier that satisfies the step and the age.
var tiers = []Tier{
	{Name: "raw", Table: "samples_raw", ResolutionSec: 1, RetentionSec: rawRetentionSec},
	{Name: "1m", Table: "samples_1m", ResolutionSec: 60, RetentionSec: oneMinRetentionSec},
	{Name: "1h", Table: "samples_1h", ResolutionSec: 3600, RetentionSec: oneHourRetentionSec},
}

// allowedSteps is the closed set of bucket widths from contract 04. A request
// can never add a step of its own.
var allowedSteps = []int64{1, 5, 10, 30, 60, 300, 900, 3600, 21600}

// PlanQuery picks the storage tier and bucket step for one window.
//
// It is pure: the caller supplies "now" and every result depends only on the
// arguments. The window must be 60s..400d, from must be before to, and from
// must not be older than 400 days. It returns a plain error for every invalid
// window; the HTTP layer maps that to 400 invalid_argument.
func PlanQuery(now, from, to int64, maxPoints int) (Plan, error) {
	if from >= to {
		return Plan{}, errors.New("from must be before to")
	}
	window := to - from
	if window < minWindowSec {
		return Plan{}, fmt.Errorf("window must be at least %d seconds", minWindowSec)
	}
	if window > maxWindowSec {
		return Plan{}, fmt.Errorf("window must be at most %d days", maxWindowSec/86400)
	}
	age := now - from
	if age > maxWindowSec {
		return Plan{}, fmt.Errorf("from must not be older than %d days", maxWindowSec/86400)
	}
	if maxPoints < 1 {
		return Plan{}, fmt.Errorf("max_points %d must be positive", maxPoints)
	}

	step := smallestStep(window, int64(maxPoints))

	// Coarsest tier that can serve the wanted step within its retention.
	for i := len(tiers) - 1; i >= 0; i-- {
		if tiers[i].ResolutionSec <= step && age <= tiers[i].RetentionSec {
			return Plan{Tier: tiers[i], StepSec: step}, nil
		}
	}

	// The window is too old for the wanted step: fall back to the finest tier
	// that still has data and never read finer than that tier stores.
	for _, tier := range tiers {
		if age <= tier.RetentionSec {
			return Plan{Tier: tier, StepSec: max(step, tier.ResolutionSec)}, nil
		}
	}

	return Plan{}, errors.New("window is older than every retained tier")
}

// smallestStep returns the smallest allowed step that keeps the bucket count
// at or below maxPoints, or the largest allowed step when none does.
func smallestStep(window, maxPoints int64) int64 {
	for _, step := range allowedSteps {
		if window/step <= maxPoints {
			return step
		}
	}
	return allowedSteps[len(allowedSteps)-1]
}
