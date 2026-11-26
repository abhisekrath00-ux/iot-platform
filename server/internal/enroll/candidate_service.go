package enroll

// CandidateService assembles the explicit experimental server boundary. It is
// disabled by default, has no environment reader and never installs its schema.
// The application must supply all trust and opt in within its own composition.
import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CandidateServiceConfig struct {
	Enabled bool
	Pool    *pgxpool.Pool
	Issuer  CandidateIssuer
}

type CandidateService struct {
	Handler http.Handler
	limiter *auth.RateLimiter
	once    sync.Once
}

func (s *CandidateService) Close() {
	s.once.Do(func() {
		if s.limiter != nil {
			s.limiter.Close()
		}
	})
}

// Validate checks trust without minting a certificate or consulting globals.
func (i CandidateIssuer) Validate() error {
	if len(i.TokenKey) < 32 || len(i.ResponseKey) != ed25519.PrivateKeySize || i.Lifetime < time.Minute || i.Lifetime > 50*time.Minute {
		return fmt.Errorf("explicit bounded candidate trust required")
	}
	seed := i.ResponseKey.Seed()
	if !ed25519.NewKeyFromSeed(seed).Equal(i.ResponseKey) {
		return fmt.Errorf("invalid response signing key")
	}
	pair, e := tls.X509KeyPair(i.CA, i.CAKey)
	if e != nil || len(pair.Certificate) != 1 {
		return fmt.Errorf("matching single device CA required")
	}
	if _, ok := pair.PrivateKey.(*ecdsa.PrivateKey); !ok {
		return fmt.Errorf("ECDSA device CA required")
	}
	ca, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil || !ca.IsCA || ca.KeyUsage&x509.KeyUsageCertSign == 0 || time.Now().Before(ca.NotBefore) || !time.Now().Add(i.Lifetime).Before(ca.NotAfter) {
		return fmt.Errorf("current signing device CA required")
	}
	return nil
}

func NewCandidateService(ctx context.Context, cfg CandidateServiceConfig) (*CandidateService, error) {
	s := &CandidateService{Handler: http.NotFoundHandler()}
	if !cfg.Enabled {
		return s, nil
	}
	if cfg.Pool == nil {
		return nil, fmt.Errorf("candidate database required")
	}
	if e := cfg.Issuer.Validate(); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Verify the pre-provisioned shape in a read-only transaction. No migration,
	// token lookup, signing, broker regeneration or gateway update occurs here.
	tx, e := cfg.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(context.Background())
	rows, e := tx.Query(ctx, `SELECT attempt_id,token_id,tenant_id,gateway_id,serial,csr_pem,csr_sha256,fingerprint,response,expires_at,revoked FROM enrollment_attempt_candidate WHERE false`)
	if e != nil {
		return nil, fmt.Errorf("candidate schema not ready: %w", e)
	}
	fields := rows.FieldDescriptions()
	rows.Close()
	// bytea, timestamptz and bool cannot be substituted with loosely cast text.
	if len(fields) != 11 {
		return nil, fmt.Errorf("candidate schema shape not ready")
	}
	for n, oid := range []uint32{25, 25, 25, 25, 25, 25, 25, 25, 17, 1184, 16} {
		if fields[n].DataTypeOID != oid {
			return nil, fmt.Errorf("candidate schema types not ready")
		}
	}
	var required int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM pg_attribute WHERE attrelid='enrollment_attempt_candidate'::regclass AND attnum>0 AND NOT attisdropped AND attnotnull AND attname=ANY($1)`, []string{"attempt_id", "token_id", "tenant_id", "gateway_id", "serial", "csr_pem", "csr_sha256", "fingerprint", "response", "expires_at", "revoked"}).Scan(&required); e != nil || required != 11 {
		return nil, fmt.Errorf("candidate required fields not ready")
	}
	var protected int
	e = tx.QueryRow(ctx, `SELECT count(DISTINCT keys) FROM (SELECT contype, (SELECT array_agg(a.attname::text ORDER BY k.ord) FROM unnest(conkey) WITH ORDINALITY k(num,ord) JOIN pg_attribute a ON a.attrelid=conrelid AND a.attnum=k.num) AS keys FROM pg_constraint WHERE conrelid='enrollment_attempt_candidate'::regclass AND contype IN ('p','u') AND convalidated AND NOT condeferrable) constraints WHERE keys IN (ARRAY['attempt_id'],ARRAY['token_id'])`).Scan(&protected)
	if e != nil || protected != 2 {
		return nil, fmt.Errorf("candidate unique attempt/token constraints not ready")
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	// Copy supplied key bytes so caller mutation cannot change active trust.
	issuer := cfg.Issuer
	issuer.CA = append([]byte(nil), issuer.CA...)
	issuer.CAKey = append([]byte(nil), issuer.CAKey...)
	issuer.TokenKey = append([]byte(nil), issuer.TokenKey...)
	issuer.ResponseKey = append(ed25519.PrivateKey(nil), issuer.ResponseKey...)
	attempts := CandidateAttempts{Pool: cfg.Pool}
	mux := http.NewServeMux()
	mux.Handle("POST /candidate/claim", attempts.CandidateHandler(false, issuer.Issue))
	mux.Handle("POST /candidate/recover", attempts.CandidateHandler(true, nil))
	// Both operations share one 5/min, burst-5 client IP budget. The existing
	// trusted-proxy boundary owns IP parsing; body and TLS checks remain inside.
	s.limiter = auth.NewRateLimiter(5, 5)
	s.Handler = auth.SecurityHeaders(s.limiter.Middleware(mux))
	return s, nil
}
