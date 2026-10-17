package tradeapi

import (
	"context"
	"database/sql"
	"math"
	"time"
)

// Trusted composition boundary, not a caller supplied timestamp or trust flag.
// No production provider or fallback to time.Now is composed by this slice.
type manualRuntimeTimeSource interface {
	sample(context.Context) (manualRuntimeSample, error)
}

type manualRuntimeSample struct {
	wall              string
	errorBound, ticks time.Duration
	provider, session string
}

type manualRuntimeClock struct {
	source               manualRuntimeTimeSource
	maximumError         time.Duration
	previous             manualRuntimeSample
	hasPrevious, invalid bool
	faultReason          string
	faultWall            string
}

func (c *manualRuntimeClock) read(ctx context.Context) (result manualRuntimeSample, readErr error) {
	defer func() {
		if recover() != nil {
			if c != nil {
				c.invalid, c.faultReason = true, "CLOCK_UNVERIFIABLE"
			}
			result = manualRuntimeSample{}
			readErr = rejectManualInput("RUNTIME_CLOCK_UNVERIFIABLE")
		}
	}()
	if c == nil || c.source == nil || ctx == nil || ctx.Err() != nil || c.invalid {
		return manualRuntimeSample{}, rejectManualInput("RUNTIME_CLOCK_UNVERIFIABLE")
	}
	s, err := c.source.sample(ctx)
	c.faultWall = s.wall
	if ctx.Err() != nil {
		return manualRuntimeSample{}, ctx.Err()
	}
	stamp, te := canonicalManualTime(s.wall)
	if err != nil || te != nil || !validID(s.provider) || !validID(s.session) || s.ticks < 0 || s.errorBound < 0 || s.errorBound > c.maximumError || c.maximumError < 0 {
		c.invalid, c.faultReason = true, "CLOCK_UNVERIFIABLE"
		return manualRuntimeSample{}, rejectManualInput("RUNTIME_CLOCK_UNVERIFIABLE")
	}
	if c.hasPrevious {
		previous, _ := canonicalManualTime(c.previous.wall)
		if stamp.Before(previous) || s.ticks < c.previous.ticks || s.provider != c.previous.provider || s.session != c.previous.session {
			c.invalid, c.faultReason = true, "CLOCK_REVERSAL"
			return manualRuntimeSample{}, rejectManualInput("RUNTIME_CLOCK_REVERSAL")
		}
	}
	if _, _, err := s.bounds(); err != nil {
		c.invalid, c.faultReason = true, "CLOCK_UNVERIFIABLE"
		return manualRuntimeSample{}, err
	}
	c.previous, c.hasPrevious = s, true
	return s, nil
}

func (s manualRuntimeSample) bounds() (time.Time, time.Time, error) {
	t, err := canonicalManualTime(s.wall)
	if err != nil || s.errorBound < 0 {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	lower, upper := t.Add(-s.errorBound), t.Add(s.errorBound)
	if lower.Year() < 1970 || upper.Year() > 9998 || lower.After(t) || upper.Before(t) {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	return lower, upper, nil
}

type manualClockState struct {
	id, environment, highWater, reason, updated string
	generation                                  int64
	fault                                       int
	faultAt                                     sql.NullString
}

func readManualRuntimeClock(ctx context.Context, q queryRow, environment string) (manualClockState, error) {
	var v manualClockState
	err := q.QueryRowContext(ctx, `SELECT id,environment,generation,high_water_at,fault_latched,fault_reason,fault_at,updated_at FROM manual_gate_clock_state WHERE chain_id=4663 AND environment=?`, environment).Scan(
		&v.id, &v.environment, &v.generation, &v.highWater, &v.fault, &v.reason, &v.faultAt, &v.updated)
	if err != nil {
		return v, err
	}
	if !validID(v.environment) || v.environment != environment || v.generation < 1 || v.generation == math.MaxInt64 || v.fault != 0 {
		return v, rejectManualInput("RUNTIME_CLOCK_LATCHED_OR_INVALID")
	}
	if _, err := canonicalManualTime(v.highWater); err != nil || v.updated != v.highWater || v.reason != "" || v.faultAt.Valid {
		return v, rejectManualInput("RUNTIME_CLOCK_STATE_INVALID")
	}
	return v, nil
}

func insertManualClockEvent(ctx context.Context, conn *sql.Conn, environment string, generation int64, high string, fault int, reason string, faultAt any, updated string) error {
	_, err := conn.ExecContext(ctx, `INSERT INTO manual_gate_clock_events(id,chain_id,environment,generation,high_water_at,fault_latched,fault_reason,fault_at,updated_at)
 VALUES('manual-clock:'||manual_artifact_bytes_sha256(hex(json_object('chain_id',4663,'environment',?,'generation',?))),4663,?,?,?,?,?,?,?)`,
		environment, generation, environment, generation, high, fault, reason, faultAt, updated)
	return err
}

// Private explicit bootstrap for the controlled harness. Existing raw history
// (even incomplete) is never initialized again, repaired or cleared.
func initializeManualRuntimeClock(ctx context.Context, store *Store, environment string, source manualRuntimeTimeSource, maximumError time.Duration) error {
	if ctx == nil || store == nil || store.db == nil || !validID(environment) || maximumError < 0 {
		return ErrInvalid
	}
	clock := manualRuntimeClock{source: source, maximumError: maximumError}
	if _, err := clock.read(ctx); err != nil {
		return err
	}
	conn, err := store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `ROLLBACK`)
	s, err := clock.read(ctx)
	if err != nil {
		return err
	}
	if err = insertManualClockEvent(ctx, conn, environment, 1, s.wall, 0, "", nil, s.wall); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `COMMIT`)
	return err
}

// Called only after the capture connection is rolled back and released.
// Failure to persist is an error, never falsely reported as durable audit.
func latchManualRuntimeClock(ctx context.Context, store *Store, environment, reason, observed string) error {
	if reason != "CLOCK_REVERSAL" && reason != "CLOCK_UNVERIFIABLE" {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	conn, err := store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `ROLLBACK`)
	v, err := readManualRuntimeClock(ctx, conn, environment)
	if err != nil {
		return err
	}
	if _, err := canonicalManualTime(observed); err != nil {
		observed = v.highWater
	}
	if err = insertManualClockEvent(ctx, conn, environment, v.generation+1, v.highWater, 1, reason, observed, observed); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `COMMIT`)
	return err
}

func checkManualClockFloor(v manualClockState, s manualRuntimeSample) error {
	floor, err := canonicalManualTime(v.highWater)
	now, ne := canonicalManualTime(s.wall)
	if err != nil || ne != nil || now.Before(floor) {
		return rejectManualInput("RUNTIME_CLOCK_REVERSAL")
	}
	return nil
}
