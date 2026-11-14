package ingestprocessing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func positionFixture(t *testing.T) (Engine, Envelope) {
	x, e := fixture(t)
	exec(t, x.Pool, `DELETE FROM flows WHERE id='f';UPDATE devices SET lat_point='p',lon_point='lon',lat=1,lon=2 WHERE id='d'`)
	return x, e
}
func readPosition(t *testing.T, x Engine) (*float64, *float64, *float64, *float64) {
	t.Helper()
	var lat, lon, latFix, lonFix *float64
	if err := x.Pool.QueryRow(t.Context(), `SELECT lat,lon,lat_fix,lon_fix FROM devices WHERE id='d' AND tenant_id='a'`).Scan(&lat, &lon, &latFix, &lonFix); err != nil {
		t.Fatal(err)
	}
	return lat, lon, latFix, lonFix
}
func processPosition(t *testing.T, x Engine, e Envelope) {
	t.Helper()
	mustAdmit(t, x, e)
	if err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, nil); err != nil {
		t.Fatal(err)
	}
}
func trailCount(t *testing.T, x Engine, want int) {
	t.Helper()
	var n int
	if err := x.Pool.QueryRow(t.Context(), `SELECT count(*) FROM device_positions WHERE device_id='d' AND tenant_id='a'`).Scan(&n); err != nil || n != want {
		t.Fatal(n, want, err)
	}
}
func TestPositionPairsStaleInvalidAndDuplicate(t *testing.T) {
	x, e := positionFixture(t)
	e.Value = 12
	processPosition(t, x, e)
	lat, lon, lf, of := readPosition(t, x)
	if lat == nil || lon == nil || *lat != 1 || *lon != 2 || lf == nil || *lf != 12 || of != nil {
		t.Fatal(lat, lon, lf, of)
	}
	trailCount(t, x, 0)
	lonEvent := e
	lonEvent.EventID = "lon"
	lonEvent.PointID = "lon"
	lonEvent.Value = 77
	lonEvent.ObservedAt = e.ObservedAt.Add(time.Second)
	processPosition(t, x, lonEvent)
	lat, lon, _, _ = readPosition(t, x)
	if *lat != 12 || *lon != 77 {
		t.Fatal(*lat, *lon)
	}
	trailCount(t, x, 1)
	// Different input ID at equal/stale axis time cannot replace the fix.
	for i, at := range []time.Time{e.ObservedAt, e.ObservedAt.Add(-time.Minute)} {
		v := e
		v.EventID = string(rune('A' + i))
		v.Value = 30
		v.ObservedAt = at
		processPosition(t, x, v)
	}
	_, _, lf, _ = readPosition(t, x)
	if *lf != 12 {
		t.Fatal(*lf)
	}
	trailCount(t, x, 1)
	v := e
	v.EventID = "bad-lat"
	v.Value = 91
	v.ObservedAt = e.ObservedAt.Add(10 * time.Second)
	processPosition(t, x, v)
	_, _, lf, _ = readPosition(t, x)
	if *lf != 12 {
		t.Fatal(*lf)
	}
	// Retained duplicate is not a new metadata stage or trail sample.
	if err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, nil); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	trailCount(t, x, 1)
}
func TestPositionWindowHandsetAndSourceRevocation(t *testing.T) {
	x, e := positionFixture(t)
	e.Value = 12
	processPosition(t, x, e)
	v := e
	v.EventID = "late-lon"
	v.PointID = "lon"
	v.Value = 77
	v.ObservedAt = e.ObservedAt.Add(3 * time.Minute)
	processPosition(t, x, v)
	lat, lon, _, _ := readPosition(t, x)
	if *lat != 1 || *lon != 2 {
		t.Fatal(*lat, *lon)
	}
	trailCount(t, x, 0)
	v.EventID = "new-lat"
	v.PointID = "p"
	v.Value = 13
	v.ObservedAt = v.ObservedAt.Add(time.Second)
	processPosition(t, x, v)
	lat, lon, _, _ = readPosition(t, x)
	if *lat != 13 || *lon != 77 {
		t.Fatal(*lat, *lon)
	}
	trailCount(t, x, 1)
	v.EventID = "source-changed"
	v.Value = 14
	v.ObservedAt = v.ObservedAt.Add(time.Second)
	mustAdmit(t, x, v)
	exec(t, x.Pool, `UPDATE devices SET lat_point=NULL,lat=9,lon=8 WHERE id='d'`)
	if err := x.ExecuteOnce(t.Context(), v.TenantID, v.EventID, v.ObservedAt, nil); !errors.Is(err, ErrRevoked) {
		t.Fatal(err)
	}
	assertState(t, x, v, "pending")
	lat, lon, _, _ = readPosition(t, x)
	if *lat != 9 || *lon != 8 {
		t.Fatal(*lat, *lon)
	}
	trailCount(t, x, 1)
}
func TestPositionTrailFaultRollsBackFixAndAttempt(t *testing.T) {
	x, e := positionFixture(t)
	e.Value = 12
	processPosition(t, x, e)
	v := e
	v.EventID = "lon"
	v.PointID = "lon"
	v.Value = 77
	v.ObservedAt = v.ObservedAt.Add(time.Second)
	mustAdmit(t, x, v)
	exec(t, x.Pool, `CREATE FUNCTION reject_position_trail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic trail write fault';END$$;CREATE TRIGGER reject_position_trail BEFORE INSERT ON device_positions FOR EACH ROW EXECUTE FUNCTION reject_position_trail()`)
	if err := x.ExecuteOnce(t.Context(), v.TenantID, v.EventID, v.ObservedAt, nil); err == nil {
		t.Fatal("trail fault accepted")
	}
	assertState(t, x, v, "pending")
	lat, lon, lf, of := readPosition(t, x)
	if *lat != 1 || *lon != 2 || *lf != 12 || of != nil {
		t.Fatal(lat, lon, lf, of)
	}
	trailCount(t, x, 0)
	exec(t, x.Pool, `DROP TRIGGER reject_position_trail ON device_positions;DROP FUNCTION reject_position_trail()`)
	if err := x.ExecuteOnce(t.Context(), v.TenantID, v.EventID, v.ObservedAt, nil); err != nil {
		t.Fatal(err)
	}
	lat, lon, _, _ = readPosition(t, x)
	if *lat != 12 || *lon != 77 {
		t.Fatal(*lat, *lon)
	}
	trailCount(t, x, 1)
}
func TestPositionAttemptFaultRollsBackAxisAndTrail(t *testing.T) {
	x, e := positionFixture(t)
	e.Value = 12
	processPosition(t, x, e)
	v := e
	v.EventID = "lon"
	v.PointID = "lon"
	v.Value = 77
	v.ObservedAt = v.ObservedAt.Add(time.Second)
	mustAdmit(t, x, v)
	exec(t, x.Pool, `CREATE FUNCTION reject_position_attempt() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic attempt update fault';END$$;CREATE TRIGGER reject_position_attempt BEFORE UPDATE ON ingest_processing_v1 FOR EACH ROW EXECUTE FUNCTION reject_position_attempt()`)
	if err := x.ExecuteOnce(t.Context(), v.TenantID, v.EventID, v.ObservedAt, nil); err == nil {
		t.Fatal("state fault accepted")
	}
	assertState(t, x, v, "pending")
	lat, lon, _, of := readPosition(t, x)
	if *lat != 1 || *lon != 2 || of != nil {
		t.Fatal(lat, lon, of)
	}
	trailCount(t, x, 0)
}
func TestPositionConcurrentAxesSerialized(t *testing.T) {
	x, e := positionFixture(t)
	e.Value = 12
	v := e
	v.EventID = "lon"
	v.PointID = "lon"
	v.Value = 77
	v.ObservedAt = e.ObservedAt.Add(time.Second)
	mustAdmit(t, x, e)
	mustAdmit(t, x, v)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, input := range []Envelope{e, v} {
		wg.Add(1)
		go func(in Envelope) {
			defer wg.Done()
			errs <- x.ExecuteOnce(t.Context(), in.TenantID, in.EventID, in.ObservedAt, nil)
		}(input)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			var pe interface{ SQLState() string }
			if !errors.As(err, &pe) || pe.SQLState() != "40001" {
				t.Fatal(err)
			}
		}
	}
	// An unstarted serialization loser can be retried; a started/completed input
	// cannot be claimed again. There were no external effects in either case.
	for _, input := range []Envelope{e, v} {
		r, err := x.Read(t.Context(), input.TenantID, input.EventID, input.ObservedAt)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == "pending" {
			if err = x.ExecuteOnce(t.Context(), input.TenantID, input.EventID, input.ObservedAt, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	lat, lon, _, _ := readPosition(t, x)
	if *lat != 12 || *lon != 77 {
		t.Fatal(*lat, *lon)
	}
	trailCount(t, x, 1)
}
func TestPositionStageCommitAndCallerRollback(t *testing.T) {
	x, e := positionFixture(t)
	e.Value = 12
	mustAdmit(t, x, e)
	rec, err := x.startAttempt(t.Context(), e.TenantID, e.EventID, e.ObservedAt, false)
	if err != nil {
		t.Fatal(err)
	}
	_, _, lf, _ := readPosition(t, x)
	if lf == nil || *lf != 12 {
		t.Fatal(lf)
	}
	if err = x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, nil); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	if err = x.finishAttempt(t.Context(), rec, "outcome_unknown"); err != nil {
		t.Fatal(err)
	}
	// Direct transaction rollback must not leak a metadata-only change.
	tx, err := x.Pool.BeginTx(t.Context(), pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	p := rec.Plan
	p.Envelope.Value = 15
	p.Envelope.ObservedAt = e.ObservedAt.Add(time.Second)
	if err = applyPosition(context.Background(), tx, p); err != nil {
		t.Fatal(err)
	}
	rollback(tx)
	_, _, lf, _ = readPosition(t, x)
	if *lf != 12 {
		t.Fatal(*lf)
	}
}
