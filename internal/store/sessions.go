package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Session represents a single charge session. EndedAt is nil while the session
// is still open. StartVehicleSoC and EndVehicleSoC are nil when unknown.
type Session struct {
	ID              int64
	StartedAt       time.Time
	EndedAt         *time.Time // nil = still open
	StartReason     string
	StopReason      string
	StartVehicleSoC *int
	EndVehicleSoC   *int
	EnergyWh        float64
	AvgChargeW      float64
	PeakChargeW     float64
}

// StartSession inserts a new open session (ended_at NULL) and returns its id.
func (s *Store) StartSession(ctx context.Context, startedAt time.Time, startReason string, startVehicleSoC *int) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO charge_sessions (started_at, start_reason, start_vehicle_soc)
		 VALUES (?, ?, ?)`,
		formatTime(startedAt), startReason, nullInt(startVehicleSoC),
	)
	if err != nil {
		return 0, fmt.Errorf("start session: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("start session: read insert id: %w", err)
	}
	return id, nil
}

// EndSession closes the open session identified by id, recording its final
// values. It returns an error if no row matches id.
func (s *Store) EndSession(ctx context.Context, id int64, endedAt time.Time, stopReason string, endVehicleSoC *int, energyWh, avgChargeW, peakChargeW float64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE charge_sessions
		    SET ended_at = ?, stop_reason = ?, end_vehicle_soc = ?,
		        energy_wh = ?, avg_charge_w = ?, peak_charge_w = ?
		  WHERE id = ?`,
		formatTime(endedAt), stopReason, nullInt(endVehicleSoC),
		energyWh, avgChargeW, peakChargeW, id,
	)
	if err != nil {
		return fmt.Errorf("end session %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("end session %d: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("end session %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// OpenSession returns the currently open session (ended_at NULL), or (nil, nil)
// if no session is open.
func (s *Store) OpenSession(ctx context.Context) (*Session, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, started_at, ended_at, start_reason, stop_reason,
		        start_vehicle_soc, end_vehicle_soc, energy_wh, avg_charge_w, peak_charge_w
		   FROM charge_sessions
		  WHERE ended_at IS NULL
		  ORDER BY started_at DESC, id DESC
		  LIMIT 1`,
	)
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("open session: %w", err)
	}
	return sess, nil
}

// UpdateSessionMetrics flushes the in-progress energy accumulators into the
// open session row so crash recovery can read back non-zero values.
func (s *Store) UpdateSessionMetrics(ctx context.Context, id int64, energyWh, avgChargeW, peakChargeW float64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE charge_sessions
		    SET energy_wh = ?, avg_charge_w = ?, peak_charge_w = ?
		  WHERE id = ? AND ended_at IS NULL`,
		energyWh, avgChargeW, peakChargeW, id,
	)
	if err != nil {
		return fmt.Errorf("update session metrics %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update session metrics %d: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("update session metrics %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// RecentSessions returns up to limit sessions, newest first.
func (s *Store) RecentSessions(ctx context.Context, limit int) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, started_at, ended_at, start_reason, stop_reason,
		        start_vehicle_soc, end_vehicle_soc, energy_wh, avg_charge_w, peak_charge_w
		   FROM charge_sessions
		  ORDER BY started_at DESC, id DESC
		  LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("recent sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("recent sessions: %w", err)
		}
		out = append(out, *sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recent sessions: iterate: %w", err)
	}
	return out, nil
}

// rowScanner is implemented by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanSession scans a single charge_sessions row, translating SQL NULLs into
// nil pointers and parsing timestamps back into UTC.
func scanSession(r rowScanner) (*Session, error) {
	var (
		sess          Session
		startedAt     string
		endedAt       sql.NullString
		stopReason    sql.NullString
		startVehicSoC sql.NullInt64
		endVehicSoC   sql.NullInt64
	)
	if err := r.Scan(
		&sess.ID, &startedAt, &endedAt, &sess.StartReason, &stopReason,
		&startVehicSoC, &endVehicSoC, &sess.EnergyWh, &sess.AvgChargeW, &sess.PeakChargeW,
	); err != nil {
		return nil, err
	}

	t, err := parseTime(startedAt)
	if err != nil {
		return nil, err
	}
	sess.StartedAt = t

	if endedAt.Valid {
		et, err := parseTime(endedAt.String)
		if err != nil {
			return nil, err
		}
		sess.EndedAt = &et
	}
	if stopReason.Valid {
		sess.StopReason = stopReason.String
	}
	sess.StartVehicleSoC = intPtr(startVehicSoC)
	sess.EndVehicleSoC = intPtr(endVehicSoC)

	return &sess, nil
}

// nullInt converts a *int into a value suitable for SQL binding: nil -> NULL.
func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// intPtr converts a scanned sql.NullInt64 into a *int (nil when NULL).
func intPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}
