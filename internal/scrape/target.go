package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fleetpulse/internal/model"
)

const (
	backoffCap            = 60 * time.Second
	truncatedWarnInterval = 5 * time.Minute
	maxSamplesBodyBytes   = 32 << 20
)

// scrapeResult tells the target loop how to schedule the next attempt.
type scrapeResult int

const (
	// scrapeOK means the scrape succeeded, including an empty one.
	scrapeOK scrapeResult = iota
	// scrapeFailed means HTTP, decoding or validation failed; back off.
	scrapeFailed
	// scrapePublishFailed means the scrape worked but publishing did not; retry
	// on the next interval without backing off.
	scrapePublishFailed
	// scrapeCanceled means shutdown interrupted the attempt.
	scrapeCanceled
)

// target pulls one exporter on its own schedule.
type target struct {
	url     string
	cfg     Config
	client  *http.Client
	pub     Publisher
	cursors *cursorStore
	sem     chan struct{}
	log     *slog.Logger
	metrics *metrics
	now     func() time.Time
	jitter  func(time.Duration) time.Duration

	backoff       time.Duration
	lastTruncWarn time.Time
}

// run scrapes until ctx is done. The first scrape happens after a random jitter
// in [0, interval), so thousands of targets do not fire at the same instant.
func (t *target) run(ctx context.Context) {
	t.backoff = t.cfg.Interval
	delay := t.jitter(t.cfg.Interval)

	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		switch t.scrape(ctx) {
		case scrapeCanceled:
			return
		case scrapeOK, scrapePublishFailed:
			t.backoff = t.cfg.Interval
			delay = t.cfg.Interval
		case scrapeFailed:
			delay = t.backoff
			t.backoff = min(t.backoff*2, backoffCap)
		}
	}
}

// scrape performs one request, drops invalid samples, publishes the rest and
// advances the cursor only after a successful publish.
func (t *target) scrape(ctx context.Context) scrapeResult {
	select {
	case t.sem <- struct{}{}:
	case <-ctx.Done():
		return scrapeCanceled
	}
	defer func() { <-t.sem }()

	since := t.cursors.get(t.url)

	// Detach from ctx so an in-flight scrape can finish during graceful
	// shutdown, but keep the per-request timeout.
	opCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), t.cfg.Timeout)
	defer cancel()

	start := t.now()
	resp, err := t.fetch(opCtx, since)
	t.metrics.scrapeMS.Observe(t.now().Sub(start))
	if err != nil {
		t.metrics.scrapesFailed.Inc()
		t.log.Warn("scrape failed", "target", t.url, "since", since, "err", err)
		return scrapeFailed
	}
	t.metrics.scrapesOK.Inc()

	if resp.Truncated {
		t.metrics.truncatedScrapes.Inc()
		t.warnTruncated()
	}

	samples := t.validSamples(resp.Samples)
	if len(samples) == 0 {
		t.advance(since, resp.NextSince)
		return scrapeOK
	}

	message := model.BusMessage{
		V:         model.BusMessageVersion,
		Device:    resp.Device,
		ScrapedAt: t.now().Unix(),
		Samples:   samples,
	}

	pubCtx, pubCancel := context.WithTimeout(context.WithoutCancel(ctx), t.cfg.Timeout)
	defer pubCancel()

	pubStart := t.now()
	err = t.pub.Publish(pubCtx, message)
	t.metrics.publishMS.Observe(t.now().Sub(pubStart))
	if err != nil {
		t.metrics.publishFailed.Inc()
		t.log.Warn("publish failed", "target", t.url, "device_id", message.Device.ID, "err", err)
		return scrapePublishFailed
	}

	t.metrics.samplesPublished.Add(int64(len(samples)))
	t.advance(since, resp.NextSince)
	return scrapeOK
}

func (t *target) fetch(ctx context.Context, since int64) (model.SamplesResponse, error) {
	endpoint, err := samplesURL(t.url, since)
	if err != nil {
		return model.SamplesResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return model.SamplesResponse{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return model.SamplesResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return model.SamplesResponse{}, fmt.Errorf("unexpected status %s", resp.Status)
	}

	var body model.SamplesResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSamplesBodyBytes)).Decode(&body); err != nil {
		return model.SamplesResponse{}, fmt.Errorf("decode samples: %w", err)
	}
	return body, nil
}

// validSamples drops samples that fail contract-01 validation and logs how many
// were dropped.
func (t *target) validSamples(samples []model.Sample) []model.Sample {
	valid := samples[:0]
	dropped := 0
	for _, sample := range samples {
		if err := sample.Validate(); err != nil {
			dropped++
			continue
		}
		valid = append(valid, sample)
	}
	if dropped > 0 {
		t.log.Debug("dropped invalid samples", "target", t.url, "dropped", dropped, "kept", len(valid))
	}
	return valid
}

// advance moves the cursor forward. It never moves backwards.
func (t *target) advance(since, next int64) {
	if next > since {
		t.cursors.set(t.url, next)
	}
}

// warnTruncated logs at most once per target per five minutes.
func (t *target) warnTruncated() {
	now := t.now()
	if !t.lastTruncWarn.IsZero() && now.Sub(t.lastTruncWarn) < truncatedWarnInterval {
		return
	}
	t.lastTruncWarn = now
	t.log.Warn("exporter buffer truncated, samples were lost", "target", t.url)
}

// samplesURL appends /v1/samples and the since query parameter to a base URL.
func samplesURL(base string, since int64) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse target url: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/samples"
	query := u.Query()
	query.Set("since", strconv.FormatInt(since, 10))
	u.RawQuery = query.Encode()
	return u.String(), nil
}

// randomJitter returns a duration in [0, interval).
func randomJitter(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(interval))) //nolint:gosec // jitter needs no cryptographic randomness
}
