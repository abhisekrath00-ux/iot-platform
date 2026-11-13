package setupconnection

import (
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"testing"
)

func TestNarrowRefusalClassification(t *testing.T) {
	for _, item := range []struct {
		err  error
		want string
	}{
		{&pgconn.PgError{Code: "28000", Message: "pg_hba.conf rejects connection for host secret"}, "hba_denied"},
		{&pgconn.PgError{Code: "28000", Message: "unrelated secret"}, "unverified"},
		{&pgconn.PgError{Code: "55000", Message: "database secret is not currently accepting connections"}, "database_closed"},
		{&pgconn.PgError{Code: "55000", Message: "unrelated secret"}, "unverified"},
		{&pgconn.PgError{Code: "28P01", Message: "private"}, "authentication_failed"},
		{errors.New("private endpoint failure"), "unverified"},
	} {
		if got := classify(item.err); got != item.want {
			t.Fatalf("got %s want %s", got, item.want)
		}
	}
}
