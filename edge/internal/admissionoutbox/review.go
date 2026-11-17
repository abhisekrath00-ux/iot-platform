package admissionoutbox

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrCollectionReview = errors.New("fresh exact pending review required; no retention started")

// PendingReview is one process-local, single-attempt review of a frozen whole
// sample. It contains no reading values, event IDs, keys or private paths. Token
// is NOT authentication, authorization or proof of durable storage. A future
// caller must independently authenticate/authorize the operator and protect it.
// Never put it in the automatic status feed or a URL. No HTTP route uses it.
// Counts can overlap retained rows after an uncertain journal clear.
type PendingReview struct {
	Token      string `json:"token"`
	Pending    int    `json:"pending"`
	Durability string `json:"durability"`
}

// ReviewPending only reviews an existing frozen sample. It never polls, retains,
// clears a journal or sends. Every fresh review replaces the prior review for
// this Collector. Reopen creates a new Collector and invalidates old tokens.
// Stop permits review/recovery; Close refuses both. No startup calls this method.
func (c *Collector) ReviewPending(ctx context.Context) (PendingReview, error) {
	if err := c.enter(ctx, false); err != nil {
		return PendingReview{}, err
	}
	defer c.leave()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) == 0 {
		c.reviewToken = ""
		return PendingReview{}, ErrCollectionReview
	}
	id, err := uuid.NewRandom()
	if err != nil {
		c.reviewToken = ""
		return PendingReview{}, err
	}
	c.reviewToken = id.String()
	return PendingReview{Token: c.reviewToken, Pending: len(c.pending), Durability: c.pendingDurability()}, nil
}

// RetainReviewed attempts local retention once for the exact reviewed pending
// generation. A valid token is consumed BEFORE any journal/store IO, including
// on failure or uncertainty. Review again before an explicit retry. Missing,
// stale, cross-collector or replayed tokens refuse without storage/source/network
// work. This never sends, polls or discards pending; it does not start a worker.
// RetainPending remains the existing low-level explicit API, not operator auth.
func (c *Collector) RetainReviewed(ctx context.Context, token string) (CollectionResult, error) {
	if err := c.enter(ctx, false); err != nil {
		return CollectionResult{}, err
	}
	defer c.leave()
	c.mu.Lock()
	if token == "" || token != c.reviewToken || len(c.pending) == 0 {
		c.mu.Unlock()
		return CollectionResult{}, ErrCollectionReview
	}
	c.reviewToken = ""
	c.mu.Unlock()
	return c.retain(ctx)
}

// pendingDurability requires c.mu. It is a classification, not independent disk
// revalidation or power-loss evidence.
func (c *Collector) pendingDurability() string {
	if len(c.pending) == 0 {
		return "none"
	}
	if c.journal == nil {
		return "memory"
	}
	if c.journalConfirmed {
		return "journal-committed"
	}
	return "journal-unconfirmed"
}
