package store

import (
	"context"
	"fmt"
	"time"
)

// Event is a single entry in the audit log.
type Event struct {
	ID         int64
	TS         time.Time
	Type       string // state_change|command|override|connection|error
	FromState  string
	ToState    string
	Action     string
	SurplusW   float64
	GridW      float64
	PVW        float64
	BatterySoC int
	BatteryW   float64
	VehicleSoC int
	ChargeW    float64
	Detail     string
}

// InsertEvent appends an event to the audit log.
func (s *Store) InsertEvent(ctx context.Context, e Event) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO events
		    (ts, type, from_state, to_state, action,
		     surplus_w, grid_w, pv_w, battery_soc, battery_w,
		     vehicle_soc, charge_w, detail)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(e.TS), e.Type, e.FromState, e.ToState, e.Action,
		e.SurplusW, e.GridW, e.PVW, e.BatterySoC, e.BatteryW,
		e.VehicleSoC, e.ChargeW, e.Detail,
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

// PruneEvents deletes all events with TS strictly before the given time,
// returning the number of rows removed.
func (s *Store) PruneEvents(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM events WHERE ts < ?`, formatTime(before))
	if err != nil {
		return 0, fmt.Errorf("prune events: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune events: rows affected: %w", err)
	}
	return n, nil
}

// RecentEvents returns up to limit events, newest first.
func (s *Store) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ts, type, from_state, to_state, action,
		        surplus_w, grid_w, pv_w, battery_soc, battery_w,
		        vehicle_soc, charge_w, detail
		   FROM events
		  ORDER BY ts DESC, id DESC
		  LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("recent events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Event
	for rows.Next() {
		var (
			e  Event
			ts string
		)
		if err := rows.Scan(
			&e.ID, &ts, &e.Type, &e.FromState, &e.ToState, &e.Action,
			&e.SurplusW, &e.GridW, &e.PVW, &e.BatterySoC, &e.BatteryW,
			&e.VehicleSoC, &e.ChargeW, &e.Detail,
		); err != nil {
			return nil, fmt.Errorf("recent events: scan: %w", err)
		}
		t, err := parseTime(ts)
		if err != nil {
			return nil, fmt.Errorf("recent events: %w", err)
		}
		e.TS = t
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recent events: iterate: %w", err)
	}
	return out, nil
}
