package store

import (
	"context"
	"fmt"
	"time"
)

// Sample is a single time-series data point used for charts.
type Sample struct {
	ID         int64
	TS         time.Time
	GridW      float64
	PVW        float64
	HomeW      float64
	BatterySoC int
	BatteryW   float64
	ChargeW    float64
	VehicleSoC int
	Charging   bool
	Mode       string
	SurplusW   float64
	State      string
}

// InsertSample appends a time-series sample.
func (s *Store) InsertSample(ctx context.Context, sm Sample) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO samples
		    (ts, grid_w, pv_w, home_w, battery_soc, battery_w,
		     charge_w, vehicle_soc, charging, mode, surplus_w, state)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(sm.TS), sm.GridW, sm.PVW, sm.HomeW, sm.BatterySoC, sm.BatteryW,
		sm.ChargeW, sm.VehicleSoC, boolToInt(sm.Charging), sm.Mode, sm.SurplusW, sm.State,
	)
	if err != nil {
		return fmt.Errorf("insert sample: %w", err)
	}
	return nil
}

// Samples returns all samples with TS in the inclusive range [from, to],
// oldest first.
func (s *Store) Samples(ctx context.Context, from, to time.Time) ([]Sample, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ts, grid_w, pv_w, home_w, battery_soc, battery_w,
		        charge_w, vehicle_soc, charging, mode, surplus_w, state
		   FROM samples
		  WHERE ts >= ? AND ts <= ?
		  ORDER BY ts ASC, id ASC`,
		formatTime(from), formatTime(to),
	)
	if err != nil {
		return nil, fmt.Errorf("samples: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Sample
	for rows.Next() {
		var (
			sm       Sample
			ts       string
			charging int64
		)
		if err := rows.Scan(
			&sm.ID, &ts, &sm.GridW, &sm.PVW, &sm.HomeW, &sm.BatterySoC, &sm.BatteryW,
			&sm.ChargeW, &sm.VehicleSoC, &charging, &sm.Mode, &sm.SurplusW, &sm.State,
		); err != nil {
			return nil, fmt.Errorf("samples: scan: %w", err)
		}
		t, err := parseTime(ts)
		if err != nil {
			return nil, fmt.Errorf("samples: %w", err)
		}
		sm.TS = t
		sm.Charging = charging != 0
		out = append(out, sm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("samples: iterate: %w", err)
	}
	return out, nil
}

// boolToInt encodes a bool as the SQLite integer 0/1.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
