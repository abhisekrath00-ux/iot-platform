package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// Bounded same-process HTTP+JWT+real-PG/cache concurrency contract, not a target
// capacity, multi-node, MQTT-ingest or full authentication lifecycle claim.
func TestIntegrationMixedCacheReadsAndProfileWrites(t *testing.T) {
	s, _ := testServer(t)
	s.cache = newCache()
	tenants := []string{"itest-cache-mixed-a", "itest-cache-mixed-b"}
	for _, tenant := range tenants {
		seed(t, s, tenant)
	}
	mux := http.NewServeMux()
	s.cached(mux, "GET /v1/profiles", s.listProfiles)
	s.cached(mux, "GET /v1/telemetry/latest", s.latestTelemetry)
	mux.HandleFunc("GET /v1/telemetry/series", s.seriesTelemetry)
	mux.HandleFunc("POST /v1/profiles", s.createProfile)
	secret := []byte("synthetic-cache-mixed-test-only")
	srv := httptest.NewServer(auth.Middleware(secret)(s.invalidateOnWrite(mux)))
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 5 * time.Second
	tokens := make([]string, len(tenants))
	for i, tenant := range tenants {
		var err error
		tokens[i], err = jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: tenant, Role: "admin", RegisteredClaims: jwt.RegisteredClaims{Subject: "test-user", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}).SignedString(secret)
		if err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var durations []time.Duration
	request := func(tenant int, method, path, body string) ([]byte, error) {
		r, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+tokens[tenant])
		r.Header.Set("Content-Type", "application/json")
		start := time.Now()
		response, err := client.Do(r)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		mu.Lock()
		durations = append(durations, time.Since(start))
		mu.Unlock()
		if err != nil || response.StatusCode >= 400 {
			return nil, fmt.Errorf("request %s %s: %d %v %s", method, path, response.StatusCode, err, data)
		}
		if !json.Valid(data) || strings.Contains(string(data), tenants[1-tenant]) {
			return nil, fmt.Errorf("invalid JSON or foreign tenant data at %s", path)
		}
		return data, nil
	}
	response, err := client.Get(srv.URL + "/v1/profiles")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("unauthenticated cached endpoint accepted request")
	}
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	started := time.Now()
	for tenant := range tenants {
		for worker := 0; worker < 2; worker++ {
			wg.Add(1)
			go func(tenant, worker int) {
				defer wg.Done()
				paths := []string{"/v1/profiles", "/v1/telemetry/latest?device_id=" + tenants[tenant] + "-dev", "/v1/telemetry/series?device_id=" + tenants[tenant] + "-dev&point_id=temp"}
				for n := 0; n < 75; n++ {
					if _, err := request(tenant, "GET", paths[(n+worker)%len(paths)], ""); err != nil {
						errors <- err
						return
					}
				}
			}(tenant, worker)
		}
		wg.Add(1)
		go func(tenant int) {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				name := fmt.Sprintf("%s-profile-%d-%d", tenants[tenant], started.UnixNano(), n)
				body := fmt.Sprintf(`{"name":%q,"driver_profile":"opcua","points":[{"id":"x","node_id":"ns=2;s=A.B","unit":"C","min":0,"max":400}]}`, name)
				if _, err := request(tenant, "POST", "/v1/profiles", body); err != nil {
					errors <- err
					return
				}
				data, err := request(tenant, "GET", "/v1/profiles", "")
				if err != nil || !strings.Contains(string(data), name) {
					errors <- fmt.Errorf("read after write was stale: %s %v", name, err)
					return
				}
			}
		}(tenant)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if s.cache.Hits.Load() == 0 || s.cache.Invalidations.Load() != 20 || len(durations) != 340 {
		t.Errorf("incomplete mixed flow: hits=%d invalidations=%d completed=%d", s.cache.Hits.Load(), s.cache.Invalidations.Load(), len(durations))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	if len(durations) > 0 {
		t.Logf("local mixed HTTP/JWT/PG baseline: tenants=2 readers=4 reads=300 writes=20 postwritechecks=20 elapsed=%s p50=%s p95=%s hits=%d misses=%d; no target capacity/MQTT/multinode/failure soak claim", time.Since(started), durations[len(durations)/2], durations[(len(durations)-1)*95/100], s.cache.Hits.Load(), s.cache.Misses.Load())
	}
}
