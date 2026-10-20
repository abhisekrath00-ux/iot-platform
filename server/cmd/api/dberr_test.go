package main

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// A failed query must never reach the client: the 500 body is generic while
// the detail goes to the server log.
func TestDbErrHidesDatabaseDetail(t *testing.T) {
	w := httptest.NewRecorder()
	dbErr(w, errors.New(`ERROR: deadlock detected (SQLSTATE 40P01) relation "alerts"`))
	if w.Code != 500 {
		t.Fatalf("code %d, want 500", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "SQLSTATE") || strings.Contains(body, "alerts") || strings.Contains(body, "deadlock") {
		t.Fatalf("database detail leaked to client: %q", body)
	}
}
