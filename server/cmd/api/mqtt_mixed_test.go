package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/golang-jwt/jwt/v5"
)

// Runs only inside the disposable certificate-required compiled-ingest fixture.
// Publications pause across one forced reconnect: clean sessions do not promise
// offline delivery. This is bounded mixed load, not a production capacity claim.
func mixedMQTTReads(t *testing.T, s *server, publisher mqtt.Client, origin string, secret []byte, reconnect func() error) {
	t.Helper()
	const tenant = "itest-mqpipe"
	client := &http.Client{Timeout: 5 * time.Second}
	token := func(tenant string) string {
		v, err := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: tenant, Role: "viewer", RegisteredClaims: jwt.RegisteredClaims{Subject: "synthetic", ExpiresAt: jwt.NewNumericDate(time.Now().Add(2 * time.Minute))}}).SignedString(secret)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	own, foreign := token(tenant), token("itest-mqother")
	var mu sync.Mutex
	durations := []time.Duration{}
	request := func(token, path string) (string, error) {
		r, err := http.NewRequest("GET", origin+path, nil)
		if err != nil {
			return "", err
		}
		r.Header.Set("Authorization", "Bearer "+token)
		start := time.Now()
		res, err := client.Do(r)
		if err != nil {
			return "", err
		}
		defer res.Body.Close()
		b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		mu.Lock()
		durations = append(durations, time.Since(start))
		mu.Unlock()
		if err != nil || res.StatusCode != 200 || !json.Valid(b) {
			return "", fmt.Errorf("mixed HTTP response status=%d err=%v", res.StatusCode, err)
		}
		return string(b), nil
	}
	start := time.Now()
	var at time.Time
	if err := s.st.Pool.QueryRow(t.Context(), `SELECT max(observed_at) FROM telemetry WHERE tenant_id=$1`, tenant).Scan(&at); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Millisecond)
	errors := make(chan error, 5)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for n := 0; n < 150; n++ {
				path := "/v1/telemetry/latest?device_id=" + tenant + "-dev"
				tok := own
				if worker%2 == 1 {
					path = "/v1/telemetry/series?device_id=" + tenant + "-dev&point_id=temp&hours=1"
				}
				if worker == 3 {
					tok = foreign
				}
				body, err := request(tok, path)
				if err != nil {
					errors <- err
					return
				}
				if worker == 3 && strings.TrimSpace(body) != "[]" {
					errors <- fmt.Errorf("mixed MQTT foreign tenant leak")
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}(worker)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 80; n++ {
			if n == 40 {
				if err := reconnect(); err != nil {
					errors <- err
					return
				}
			}
			payload, _ := json.Marshal(map[string]any{"event_id": fmt.Sprintf("mq-mixed-%03d", n), "device_id": tenant + "-dev", "point_id": "temp", "observed_at": at.Add(time.Duration(n) * time.Millisecond), "value": 5000 + n, "unit": "C", "schema_version": 1})
			for repeat := 0; repeat < 1+boolInt(n%10 == 0); repeat++ {
				tok := publisher.Publish("t/"+tenant+"/g/"+tenant+"-gw/telemetry", 1, false, payload)
				if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
					errors <- fmt.Errorf("mixed publication failed: %v", tok.Error())
					return
				}
			}
			time.Sleep(15 * time.Millisecond)
		}
	}()
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	end := time.Now().Add(8 * time.Second)
	for {
		var count, unique int
		if err := s.st.Pool.QueryRow(t.Context(), `SELECT count(*),count(DISTINCT event_id) FROM telemetry WHERE tenant_id=$1 AND event_id LIKE 'mq-mixed-%'`, tenant).Scan(&count, &unique); err != nil {
			t.Fatal(err)
		}
		body, err := request(own, "/v1/telemetry/latest?device_id="+tenant+"-dev")
		if err == nil && count == 80 && unique == 80 && strings.Contains(body, `"value":5079`) {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("mixed final reading not fresh/deduplicated count=%d unique=%d err=%v body=%s", count, unique, err, body)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var foreignCount int
	if err := s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM telemetry WHERE tenant_id<>$1 AND event_id LIKE 'mq-mixed-%'`, tenant).Scan(&foreignCount); err != nil || foreignCount != 0 {
		t.Fatal("mixed foreign telemetry", foreignCount, err)
	}
	if s.cache.Hits.Load() == 0 {
		t.Fatal("mixed latest reads never used cache")
	}
	var max time.Duration
	for _, d := range durations {
		if d > max {
			max = d
		}
	}
	pool := s.st.Pool.Stat()
	t.Logf("bounded mixed mTLS/compiled-ingest/PG/cached HTTP:80events+8duplicates;4JWTreaders x150requests;publication pauses across reconnect;80unique rows/final5079fresh;elapsed=%s requests=%d maxHTTP=%s cachehits=%d pooltotal=%d max=%d emptyacquires=%d; no target capacity/report/background/DB-outage/hardware claim", time.Since(start), len(durations), max, s.cache.Hits.Load(), pool.TotalConns(), pool.MaxConns(), pool.EmptyAcquireCount())
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
