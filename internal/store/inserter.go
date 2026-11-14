package store

import (
	"context"
	"fmt"
	"sort"
	"time"
)

const (
	samplesTable = "samples_raw"
	devicesTable = "devices"
)

// Inserter writes a batch of sample and device rows. The writer depends on this
// interface so its batching logic can be tested without ClickHouse.
type Inserter interface {
	Insert(ctx context.Context, rows []SampleRow, devices []DeviceRow) error
}

// Insert writes one batch to samples_raw and one batch to devices. ClickHouse
// has no cross-table transaction: if the devices batch fails after the samples
// batch succeeded, the retry re-inserts the samples, which ADR-0007 accepts.
func (c *Client) Insert(ctx context.Context, rows []SampleRow, devices []DeviceRow) error {
	if len(rows) == 0 && len(devices) == 0 {
		return nil
	}
	if err := c.insertSamples(ctx, rows); err != nil {
		return err
	}
	return c.insertDevices(ctx, devices)
}

func (c *Client) insertSamples(ctx context.Context, rows []SampleRow) error {
	if len(rows) == 0 {
		return nil
	}

	sorted := make([]SampleRow, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].DeviceID != sorted[j].DeviceID {
			return sorted[i].DeviceID < sorted[j].DeviceID
		}
		if sorted[i].Sensor != sorted[j].Sensor {
			return sorted[i].Sensor < sorted[j].Sensor
		}
		return sorted[i].TS < sorted[j].TS
	})

	batch, err := c.conn.PrepareBatch(ctx, "INSERT INTO "+samplesTable+" (device_id, sensor, ts, value)")
	if err != nil {
		return fmt.Errorf("store: prepare %s batch: %w", samplesTable, err)
	}
	for _, row := range sorted {
		if err := batch.Append(row.DeviceID, string(row.Sensor), time.Unix(row.TS, 0).UTC(), row.Value); err != nil {
			return fmt.Errorf("store: append %s row: %w", samplesTable, err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("store: send %s batch: %w", samplesTable, err)
	}
	return nil
}

func (c *Client) insertDevices(ctx context.Context, devices []DeviceRow) error {
	if len(devices) == 0 {
		return nil
	}

	// One timestamp for the whole batch: ReplacingMergeTree keeps the row with
	// the highest updated_at per device_id.
	now := time.Now().UTC()

	batch, err := c.conn.PrepareBatch(ctx, "INSERT INTO "+devicesTable+" (device_id, hostname, os, arch, last_seen, updated_at)")
	if err != nil {
		return fmt.Errorf("store: prepare %s batch: %w", devicesTable, err)
	}
	for _, device := range devices {
		err := batch.Append(
			device.Device.ID,
			device.Device.Hostname,
			device.Device.OS,
			device.Device.Arch,
			time.Unix(device.LastSeen, 0).UTC(),
			now,
		)
		if err != nil {
			return fmt.Errorf("store: append %s row: %w", devicesTable, err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("store: send %s batch: %w", devicesTable, err)
	}
	return nil
}
