package simulate

import (
	"math"

	"fleetpulse/internal/sensor"
)

const (
	secondsPerDay = int64(86400)

	// diskGrowthEpoch anchors the linear disk-fill model so values stay in a
	// realistic range for current timestamps instead of saturating at 99.
	diskGrowthEpoch  = int64(1760000000)
	diskGrowthPerDay = 0.02
)

// Hash salts keep the independent parameters of one sensor independent.
const (
	saltCPUBase uint64 = iota + 1
	saltCPUAmplitude
	saltCPUPhase
	saltCPUNoise
	saltCPUBurst
	saltMemoryBase
	saltMemoryPhase
	saltMemoryNoise
	saltDiskBase
	saltDiskBusyBase
	saltDiskBusyNoise
	saltNetInBase
	saltNetOutBase
	saltNetInNoise
	saltNetOutNoise
	saltNetInBurst
	saltNetOutBurst
	saltFanless
)

// hash64 folds the signal coordinates into a well-distributed 64-bit value.
// It is FNV-1a style mixing finished with splitmix64, so no global random
// state is needed and every call with the same inputs returns the same bits.
func hash64(seed int64, device int, s sensor.Sensor, salt, extra uint64) uint64 {
	h := uint64(seed) ^ 0xcbf29ce484222325 //nolint:gosec // bits are hashed, not used as a size
	h ^= uint64(device)                    //nolint:gosec // device is non-negative; bits are hashed
	h *= 0x100000001b3
	h ^= uint64(s.ID()) //nolint:gosec // sensor IDs are 0..7; bits are hashed
	h *= 0x100000001b3
	h ^= salt
	h *= 0x100000001b3
	h ^= extra
	h *= 0x100000001b3
	return splitmix64(h)
}

func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// unit returns a deterministic value in [0, 1) for the given coordinates.
func unit(seed int64, device int, s sensor.Sensor, salt, extra uint64) float64 {
	return float64(hash64(seed, device, s, salt, extra)>>11) / float64(uint64(1)<<53)
}

// valueNoise returns smooth noise in [-1, 1], interpolated between the hashes
// of two adjacent buckets. A 30-second bucket makes the signal wobble without
// looking like white noise.
func valueNoise(seed int64, device int, s sensor.Sensor, salt uint64, ts, bucketSeconds int64) float64 {
	bucket := floorDiv(ts, bucketSeconds)
	frac := float64(ts-bucket*bucketSeconds) / float64(bucketSeconds)
	start := unit(seed, device, s, salt, uint64(bucket)) //nolint:gosec // bucket bits are hashed
	end := unit(seed, device, s, salt, uint64(bucket+1)) //nolint:gosec // bucket bits are hashed
	t := frac * frac * (3 - 2*frac)
	return (start+(end-start)*t)*2 - 1
}

// burstLevel reports whether ts falls inside a deterministic burst window and
// returns a level in [0, 1]. Previous epochs are checked too, so a burst may
// cross an epoch boundary.
func burstLevel(seed int64, device int, s sensor.Sensor, salt uint64, ts, period, minDuration, maxDuration int64) (float64, bool) {
	epoch := floorDiv(ts, period)
	level := 0.0
	active := false

	for _, candidate := range [2]int64{epoch - 1, epoch} {
		offset := int64(hash64(seed, device, s, salt, uint64(candidate)) % uint64(period)) //nolint:gosec // epoch bits are hashed
		duration := minDuration
		if span := maxDuration - minDuration; span > 0 {
			duration += int64(hash64(seed, device, s, salt^0x5bd1e995, uint64(candidate)) % uint64(span+1)) //nolint:gosec // epoch bits are hashed
		}

		start := candidate*period + offset
		if ts < start || ts >= start+duration {
			continue
		}

		active = true
		if candidateLevel := unit(seed, device, s, salt^0x9e3779b9, uint64(candidate)); candidateLevel > level { //nolint:gosec // epoch bits are hashed
			level = candidateLevel
		}
	}

	return level, active
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func positiveMod(a, b int64) int64 {
	m := a % b
	if m < 0 {
		m += b
	}
	return m
}

func dailySine(ts int64, phase float64) float64 {
	day := float64(positiveMod(ts, secondsPerDay)) / float64(secondsPerDay)
	return math.Sin(2*math.Pi*day + phase)
}

func cpuBase(seed int64, device int) float64 {
	return 10 + 35*unit(seed, device, sensor.CPUUsage, saltCPUBase, 0)
}

func cpuAmplitude(seed int64, device int) float64 {
	return 5 + 20*unit(seed, device, sensor.CPUUsage, saltCPUAmplitude, 0)
}

func cpuPhase(seed int64, device int) float64 {
	return 2 * math.Pi * unit(seed, device, sensor.CPUUsage, saltCPUPhase, 0)
}

func cpuValue(seed int64, device int, ts int64) float64 {
	value := cpuBase(seed, device) + cpuAmplitude(seed, device)*dailySine(ts, cpuPhase(seed, device))
	value += 8 * valueNoise(seed, device, sensor.CPUUsage, saltCPUNoise, ts, 30)
	if level, active := burstLevel(seed, device, sensor.CPUUsage, saltCPUBurst, ts, 6*3600, 120, 600); active {
		value += 30 + 20*level
	}
	return value
}

func memoryValue(seed int64, device int, ts int64) float64 {
	base := 30 + 40*unit(seed, device, sensor.MemoryUsage, saltMemoryBase, 0)
	phase := 2 * math.Pi * unit(seed, device, sensor.MemoryUsage, saltMemoryPhase, 0)
	value := base + 5*dailySine(ts, phase)
	value += 0.5 * valueNoise(seed, device, sensor.MemoryUsage, saltMemoryNoise, ts, 60)
	return value
}

func diskValue(seed int64, device int, ts int64) float64 {
	value := 30 + 40*unit(seed, device, sensor.DiskUsage, saltDiskBase, 0)
	value += float64(ts-diskGrowthEpoch) / float64(secondsPerDay) * diskGrowthPerDay
	if value > 99 {
		value = 99
	}
	return value
}

func diskBusyValue(seed int64, device int, ts int64) float64 {
	base := 2 + 8*unit(seed, device, sensor.DiskBusy, saltDiskBusyBase, 0)
	value := base + 4*valueNoise(seed, device, sensor.DiskBusy, saltDiskBusyNoise, ts, 15)
	// Correlate with CPU bursts: the same window and hash as cpuValue.
	if level, active := burstLevel(seed, device, sensor.CPUUsage, saltCPUBurst, ts, 6*3600, 120, 600); active {
		value += 0.3 * (30 + 20*level)
	}
	return value
}

func networkValue(seed int64, device int, s sensor.Sensor, baseSalt, noiseSalt, burstSalt uint64, ts int64) float64 {
	base := 2 + 18*unit(seed, device, s, baseSalt, 0)
	value := base + 3*valueNoise(seed, device, s, noiseSalt, ts, 30)
	if level, active := burstLevel(seed, device, s, burstSalt, ts, 300, 10, 60); active {
		value += (80 - base) * (0.5 + 0.5*level)
	}
	return value
}

func fanValue(seed int64, device int, ts int64) float64 {
	sum := 0.0
	for i := range 5 {
		sum += cpuValue(seed, device, ts-int64(i)*60)
	}
	return 25 + 0.6*(sum/5)
}

// roundClamp makes a generated value safe for the wire: finite, inside
// [0, 100] and rounded to two decimals like the real exporter.
func roundClamp(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	value = math.Min(math.Max(value, 0), 100)
	return math.Round(value*100) / 100
}
