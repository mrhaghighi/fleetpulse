package query

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fleetpulse/internal/sensor"
)

const (
	maxDevicesPerQuery  = 8
	maxSeriesPerQuery   = 24
	defaultDevicesLimit = 50
	maxDevicesLimit     = 200
)

// rangeWindows maps the contract-04 range names to their length in seconds.
var rangeWindows = map[string]int64{
	"5m":  5 * 60,
	"15m": 15 * 60,
	"1h":  3600,
	"6h":  6 * 3600,
	"24h": 24 * 3600,
	"7d":  7 * 86400,
	"30d": 30 * 86400,
	"90d": 90 * 86400,
	"1y":  365 * 86400,
}

// SeriesParams is a parsed GET /api/v1/series request.
type SeriesParams struct {
	Devices   []string
	Sensors   []sensor.Sensor
	From      int64
	To        int64
	MaxPoints int
}

// ParseSeriesParams parses and validates the series query string. Every error
// is an invalid_argument: the HTTP layer returns it verbatim with status 400.
func ParseSeriesParams(r *http.Request, now time.Time) (SeriesParams, error) {
	values := r.URL.Query()

	devices, err := parseDeviceList(values.Get("devices"))
	if err != nil {
		return SeriesParams{}, err
	}
	sensors, err := parseSensorList(values.Get("sensors"))
	if err != nil {
		return SeriesParams{}, err
	}
	if len(devices)*len(sensors) > maxSeriesPerQuery {
		return SeriesParams{}, fmt.Errorf("series: devices × sensors must be at most %d", maxSeriesPerQuery)
	}

	from, to, err := parseWindow(values, now.Unix())
	if err != nil {
		return SeriesParams{}, err
	}
	maxPoints, err := parseMaxPoints(values.Get("max_points"))
	if err != nil {
		return SeriesParams{}, err
	}

	return SeriesParams{
		Devices:   devices,
		Sensors:   sensors,
		From:      from,
		To:        to,
		MaxPoints: maxPoints,
	}, nil
}

// DevicesParams is a parsed GET /api/v1/devices request.
type DevicesParams struct {
	Query  string
	Limit  int
	Offset int
}

// ParseDevicesParams parses and validates the devices query string. A limit
// above the maximum is clamped; a non-positive or non-numeric limit is an
// error, as is a negative offset.
func ParseDevicesParams(r *http.Request) (DevicesParams, error) {
	values := r.URL.Query()

	limit := defaultDevicesLimit
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return DevicesParams{}, errors.New("limit: must be a positive integer")
		}
		limit = min(parsed, maxDevicesLimit)
	}

	offset := 0
	if raw := values.Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return DevicesParams{}, errors.New("offset: must be a non-negative integer")
		}
		offset = parsed
	}

	return DevicesParams{Query: values.Get("q"), Limit: limit, Offset: offset}, nil
}

func parseWindow(values url.Values, now int64) (int64, int64, error) {
	rangeName := values.Get("range")
	fromText := values.Get("from")
	toText := values.Get("to")

	switch {
	case rangeName != "" && fromText != "":
		return 0, 0, errors.New("range and from are mutually exclusive")
	case rangeName != "" && toText != "":
		return 0, 0, errors.New("range and to are mutually exclusive")
	case rangeName != "":
		window, ok := rangeWindows[rangeName]
		if !ok {
			return 0, 0, fmt.Errorf("range: unknown range %q", rangeName)
		}
		return now - window, now, nil
	case fromText == "":
		return 0, 0, errors.New("range or from is required")
	}

	from, err := parseUnix("from", fromText)
	if err != nil {
		return 0, 0, err
	}
	to := now
	if toText != "" {
		to, err = parseUnix("to", toText)
		if err != nil {
			return 0, 0, err
		}
	}
	return from, to, nil
}

func parseUnix(name, text string) (int64, error) {
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a unix timestamp", name, text)
	}
	return value, nil
}

func parseDeviceList(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("devices: at least one device is required")
	}

	parts := strings.Split(raw, ",")
	if len(parts) > maxDevicesPerQuery {
		return nil, fmt.Errorf("devices: at most %d devices", maxDevicesPerQuery)
	}

	devices := make([]string, 0, len(parts))
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if id == "" {
			return nil, errors.New("devices: empty device id")
		}
		devices = append(devices, id)
	}
	return devices, nil
}

func parseSensorList(raw string) ([]sensor.Sensor, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("sensors: at least one sensor is required")
	}

	parts := strings.Split(raw, ",")
	if len(parts) > len(sensor.All()) {
		return nil, fmt.Errorf("sensors: at most %d sensors", len(sensor.All()))
	}

	sensors := make([]sensor.Sensor, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, errors.New("sensors: empty sensor name")
		}
		parsed, err := sensor.Parse(name)
		if err != nil {
			return nil, fmt.Errorf("sensors: unknown sensor %q", name)
		}
		sensors = append(sensors, parsed)
	}
	return sensors, nil
}

func parseMaxPoints(raw string) (int, error) {
	if raw == "" {
		return defaultMaxPoints, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("max_points: %q is not an integer", raw)
	}
	if value < minMaxPoints || value > maxMaxPoints {
		return 0, fmt.Errorf("max_points: must be between %d and %d", minMaxPoints, maxMaxPoints)
	}
	return value, nil
}
