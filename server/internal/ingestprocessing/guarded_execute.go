package ingestprocessing

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/authorityschema"
	"github.com/jackc/pgx/v5"
)

var ErrGuardedExecution = errors.New("staged execution requires explicit current gateway and durable denial authority")

// GuardedExecutor is an explicit single-batch API, not a worker or startup path.
// It grants no sender/recipient permission. Only synthetic acceptance calls it.
// Frozen routes are prospective authority, NOT proof of a record's original TLS
// provenance. Direct Engine/DB writers remain outside this boundary.
// New opens no socket and performs no migration, claim, or external call.
// The caller must keep its pool and Sender alive until ExecuteOnce returns.
type GuardedExecutor struct {
	engine Engine
	slots  chan struct{}
}
type GuardedExecutionConfig struct {
	Enabled            bool
	Engine             Engine
	Routes             []CertificateRoute
	MigrationDirectory string
}

func NewGuardedExecutor(ctx context.Context, c GuardedExecutionConfig) (*GuardedExecutor, error) {
	if !c.Enabled {
		return nil, ErrDisabled
	}
	if ctx == nil || !c.Engine.Enabled || c.Engine.Pool == nil || len(c.Routes) == 0 || len(c.Routes) > 256 {
		return nil, ErrGuardedExecution
	}
	routes := make(map[string][32]byte, len(c.Routes))
	leaves := make(map[[32]byte]bool, len(c.Routes))
	for _, r := range c.Routes {
		if r.LeafSHA256 == ([32]byte{}) || !validHTTPTopic(r.Topic) {
			return nil, ErrGuardedExecution
		}
		// One exact gateway route, no ambiguous rotation or caller-slice aliasing.
		if _, ok := routes[r.Topic]; ok {
			return nil, ErrGuardedExecution
		}
		if leaves[r.LeafSHA256] {
			return nil, ErrGuardedExecution
		}
		leaves[r.LeafSHA256] = true
		routes[r.Topic] = r.LeafSHA256
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := authorityschema.Inspect(ctx, c.Engine.Pool, c.MigrationDirectory); err != nil {
		return nil, ErrGuardedExecution
	}
	tx, err := c.Engine.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, ErrGuardedExecution
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='5s';SET LOCAL statement_timeout='10s'"); err != nil {
		return nil, ErrGuardedExecution
	}
	// Sorted ordering matches guarded admission preflight and avoids map-dependent
	// lock ordering. Selected witness/provenance/full restore acceptance is separate.
	for _, topic := range sortedExecutionTopics(routes) {
		p := strings.Split(topic, "/")
		e := Envelope{TenantID: p[1], GatewayID: p[3]}
		if err = executionAuthority(ctx, tx, e, routes); err != nil {
			return nil, ErrGuardedExecution
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, ErrGuardedExecution
	}
	x := c.Engine
	x.executionRoutes = routes
	return &GuardedExecutor{engine: x, slots: make(chan struct{}, 4)}, nil
}

// ExecuteOnce attempts ONE explicitly selected retained batch, at most four
// concurrent calls per constructed executor. No queue, scan, rearm or retry.
// Its original 30s deadline includes claim, delays, authorization and sender I/O;
// the existing one-attempt sender receives at most 10s per occurrence. A separate
// best-effort unknown checkpoint may take 3s after cancellation. Noncooperative
// Senders cannot be forcibly stopped; the caller MUST await actual return.
// A denial completed before claim leaves pending/no history. After claim, any
// refusal/cancellation is outcome_unknown, even if no effect has started yet.
// Locks are NOT held over provider I/O: revocation cannot undo or stop an already
// entered request, nor close the interval between final check and SendOnce.
func (g *GuardedExecutor) ExecuteOnce(ctx context.Context, tenant, event string, at time.Time, s Sender) error {
	if g == nil || ctx == nil || g.slots == nil || len(g.engine.executionRoutes) == 0 {
		return ErrGuardedExecution
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case g.slots <- struct{}{}:
		defer func() { <-g.slots }()
	default:
		return ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return g.engine.ExecuteOnce(ctx, tenant, event, at, s)
}
func executionAuthority(ctx context.Context, tx pgx.Tx, e Envelope, routes map[string][32]byte) error {
	if routes == nil {
		return nil
	} // Historical Engine is explicitly NOT upgraded.
	leaf, ok := routes["t/"+e.TenantID+"/g/"+e.GatewayID+"/telemetry"]
	if !ok {
		return ErrGuardedExecution
	}
	if err := guardDurableLeaf(ctx, tx, leaf); err != nil {
		return err
	}
	return guardGatewayAuthority(ctx, tx, e, leaf)
}

func sortedExecutionTopics(routes map[string][32]byte) []string {
	topics := make([]string, 0, len(routes))
	for topic := range routes {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	return topics
}
