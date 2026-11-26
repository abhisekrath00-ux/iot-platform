package main

import (
	"context"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/enroll"
	"net/http"
)

// registerCandidateEnrollment is the single product router composition point.
// main supplies a zero config: no environment switch can enable this candidate.
// Only isolated acceptance callers supply explicit synthetic trust and schema.
func registerCandidateEnrollment(ctx context.Context, mux *http.ServeMux, cfg enroll.CandidateServiceConfig) (func(), error) {
	service, e := enroll.NewCandidateService(ctx, cfg)
	if e != nil {
		return nil, e
	}
	mux.Handle("/candidate/", service.Handler)
	return service.Close, nil
}
