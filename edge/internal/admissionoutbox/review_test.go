package admissionoutbox

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
)

func TestReviewedRecoveryExactOnceNoAutomaticWork(t *testing.T) {
	s := journalSession(t)
	occupant := fillOne(t, s)
	polls := 0
	cfg := collectorConfig(s, journalReader(&polls))
	jc := journalConfig(t)
	c := journalCollector(t, cfg, jc)
	if _, err := c.ReviewPending(t.Context()); !errors.Is(err, ErrCollectionReview) {
		t.Fatal(err)
	}
	if _, err := c.PollOnce(t.Context()); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	frozen := append([]Reading(nil), c.pending...)
	a, err := c.ReviewPending(t.Context())
	if err != nil || a.Pending != 2 || a.Durability != "journal-committed" || a.Token == "" {
		t.Fatal(a, err)
	}
	b, err := c.ReviewPending(t.Context())
	if err != nil || b.Token == a.Token {
		t.Fatal(b, err)
	}
	for _, token := range []string{"", "wrong", a.Token} {
		if _, err := c.RetainReviewed(t.Context(), token); !errors.Is(err, ErrCollectionReview) {
			t.Fatal(err)
		}
	}
	// Capacity failure consumes the reviewed attempt, not the frozen input.
	if _, err := c.RetainReviewed(t.Context(), b.Token); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	if _, err := c.RetainReviewed(t.Context(), b.Token); !errors.Is(err, ErrCollectionReview) {
		t.Fatal(err)
	}
	review, err := c.ReviewPending(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec("DELETE FROM admission_inputs WHERE sequence=?", occupant.Sequence); err != nil {
		t.Fatal(err)
	}
	if r, err := c.RetainReviewed(t.Context(), review.Token); err != nil || r.Retained != 2 || r.Pending != 0 {
		t.Fatal(r, err)
	}
	if _, err := c.RetainReviewed(t.Context(), review.Token); !errors.Is(err, ErrCollectionReview) {
		t.Fatal(err)
	}
	rows, err := s.store.Next(t.Context(), 2)
	if err != nil || len(rows) != 2 || rows[0].Reading != frozen[0] || rows[1].Reading != frozen[1] || polls != 1 {
		t.Fatal(rows, polls, err)
	}
	if st, err := s.Status(t.Context()); err != nil || st.LastSend != nil {
		t.Fatal(st, err)
	}
	data, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{frozen[0].EventID, jc.Path, s.tenant, s.gateway, "observed_at", "value"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("review leaked private input", secret)
		}
	}
	status, _ := json.Marshal(c.Status())
	if strings.Contains(string(status), review.Token) {
		t.Fatal("review capability in automatic status")
	}
}

func TestReviewedRecoveryReopenAndOtherCollectorRefuse(t *testing.T) {
	s := journalSession(t)
	fillOne(t, s)
	polls := 0
	cfg := collectorConfig(s, journalReader(&polls))
	jc := journalConfig(t)
	c := journalCollector(t, cfg, jc)
	c.PollOnce(t.Context())
	review, err := c.ReviewPending(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c.Close(t.Context())
	if _, err := c.RetainReviewed(t.Context(), review.Token); !errors.Is(err, ErrSessionClosed) {
		t.Fatal(err)
	}
	if _, err := c.ReviewPending(t.Context()); !errors.Is(err, ErrSessionClosed) {
		t.Fatal(err)
	}
	c = journalCollector(t, cfg, jc)
	if _, err := c.RetainReviewed(t.Context(), review.Token); !errors.Is(err, ErrCollectionReview) {
		t.Fatal(err)
	}
	review, err = c.ReviewPending(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	other := journalCollector(t, cfg, journalConfig(t))
	other.PollOnce(t.Context())
	if _, err := other.RetainReviewed(t.Context(), review.Token); !errors.Is(err, ErrCollectionReview) {
		t.Fatal(err)
	}
	// Existing explicit low-level retry also invalidates the review, even on error.
	if _, err := c.RetainPending(t.Context()); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	if _, err := c.RetainReviewed(t.Context(), review.Token); !errors.Is(err, ErrCollectionReview) {
		t.Fatal(err)
	}
	if polls != 2 {
		t.Fatal(polls)
	}
}

func TestReviewedRecoveryBusyAndConcurrentSingleAttempt(t *testing.T) {
	s := journalSession(t)
	fillOne(t, s)
	started, release := make(chan struct{}), make(chan struct{})
	reader := pollFunc(func(context.Context) ([]driver.Reading, error) {
		close(started)
		<-release
		return sourceSamples(), nil
	})
	c := journalCollector(t, collectorConfig(s, reader), journalConfig(t))
	done := make(chan error, 1)
	go func() { _, err := c.PollOnce(t.Context()); done <- err }()
	<-started
	if _, err := c.ReviewPending(t.Context()); !errors.Is(err, ErrCollectorBusy) {
		t.Fatal(err)
	}
	if _, err := c.RetainReviewed(t.Context(), "wrong"); !errors.Is(err, ErrCollectorBusy) {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	r, err := c.ReviewPending(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.RetainReviewed(ctx, r.Token); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := c.RetainReviewed(t.Context(), r.Token); results <- err })
	}
	wg.Wait()
	close(results)
	attempted := 0
	for err := range results {
		if errors.Is(err, ErrCapacity) {
			attempted++
		} else if !errors.Is(err, ErrCollectorBusy) && !errors.Is(err, ErrCollectionReview) {
			t.Fatal(err)
		}
	}
	if attempted != 1 {
		t.Fatal("more than one reviewed attempt", attempted)
	}
}

func TestReviewedRecoveryMemoryAndUnconfirmedAreNotDurable(t *testing.T) {
	for _, journal := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "journal-unconfirmed"}[journal], func(t *testing.T) {
			s := journalSession(t)
			fillOne(t, s)
			polls := 0
			cfg := collectorConfig(s, journalReader(&polls))
			var c *Collector
			if journal {
				c = journalCollector(t, cfg, journalConfig(t))
				if _, err := c.journal.db.Exec("CREATE TRIGGER reject_save BEFORE INSERT ON collection_pending BEGIN SELECT RAISE(ABORT,'fixture'); END"); err != nil {
					t.Fatal(err)
				}
			} else {
				var err error
				c, err = NewCollector(cfg)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { c.Close(context.Background()) })
			}
			if _, err := c.PollOnce(t.Context()); err == nil {
				t.Fatal("expected pending")
			}
			r, err := c.ReviewPending(t.Context())
			if err != nil || r.Durability != map[bool]string{false: "memory", true: "journal-unconfirmed"}[journal] {
				t.Fatal(r, err)
			}
			if _, err := c.RetainReviewed(t.Context(), r.Token); err == nil {
				t.Fatal("expected unresolved")
			}
			if _, err := c.RetainReviewed(t.Context(), r.Token); !errors.Is(err, ErrCollectionReview) {
				t.Fatal(err)
			}
			if polls != 1 {
				t.Fatal(polls)
			}
		})
	}
}

func TestReviewedRecoveryClearUncertaintyRequiresFreshReview(t *testing.T) {
	s := journalSession(t)
	polls := 0
	c := journalCollector(t, collectorConfig(s, journalReader(&polls)), journalConfig(t))
	if _, err := c.journal.db.Exec("CREATE TRIGGER clear_fault BEFORE DELETE ON collection_pending BEGIN SELECT RAISE(ABORT,'fixture'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PollOnce(t.Context()); err == nil {
		t.Fatal("expected clear uncertainty")
	}
	before, err := s.store.Next(t.Context(), 2)
	if err != nil || len(before) != 2 {
		t.Fatal(before, err)
	}
	review, err := c.ReviewPending(t.Context())
	if err != nil || review.Pending != 2 || review.Durability != "journal-committed" {
		t.Fatal(review, err)
	}
	if _, err := c.RetainReviewed(t.Context(), review.Token); err == nil {
		t.Fatal("expected clear uncertainty")
	}
	if _, err := c.RetainReviewed(t.Context(), review.Token); !errors.Is(err, ErrCollectionReview) {
		t.Fatal(err)
	}
	if _, err := c.journal.db.Exec("DROP TRIGGER clear_fault"); err != nil {
		t.Fatal(err)
	}
	review, err = c.ReviewPending(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if result, err := c.RetainReviewed(t.Context(), review.Token); err != nil || result.Pending != 0 || result.Retained != 2 {
		t.Fatal(result, err)
	}
	after, err := s.store.Next(t.Context(), 2)
	if err != nil || len(after) != 2 || !reflect.DeepEqual(before, after) || polls != 1 {
		t.Fatal(before, after, polls, err)
	}
}
