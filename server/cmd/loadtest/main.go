// loadtest simulates a gateway cohort against a running stack and reports
// ingest and query latency against the SLOs in docs/slo.md.
//
// It runs inside the deployment network (air-gap safe: MQTT + HTTP only):
//
//	go run ./cmd/loadtest -jwt-secret "$JWT_SIGNING_SECRET" \
//	  -gateways 100 -rate 1 -duration 60s
//
// Exit code is non-zero when any SLO is breached or messages are lost.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/loadstats"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/golang-jwt/jwt/v5"
)

type marker struct {
	seq  int
	sent time.Time
}

type gwResult struct {
	sent      int
	confirmed int
	latencies []time.Duration
}

func mintToken(secret, tenant string) (string, error) {
	c := jwt.MapClaims{"tenant_id": tenant, "sub": "loadtest", "role": "viewer",
		"exp": time.Now().Add(2 * time.Hour).Unix(), "iat": time.Now().Unix()}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
}

func main() {
	var (
		apiBase   = flag.String("api", "http://localhost:8000", "API base URL")
		mqttHost  = flag.String("mqtt-host", "localhost", "MQTT host")
		mqttPort  = flag.Int("mqtt-port", 1883, "MQTT port")
		secret    = flag.String("jwt-secret", os.Getenv("JWT_SIGNING_SECRET"), "JWT signing secret (mints a viewer token)")
		tenant    = flag.String("tenant", "demo", "tenant id")
		gateways  = flag.Int("gateways", 100, "simulated gateways")
		rate      = flag.Float64("rate", 1, "messages per second per gateway")
		duration  = flag.Duration("duration", 60*time.Second, "publish duration")
		qWorkers  = flag.Int("query-workers", 10, "concurrent query workers")
		sloIngest = flag.Float64("slo-ingest-ms", 5000, "ingest p95 budget (publish -> queryable)")
		sloQuery  = flag.Float64("slo-query-ms", 500, "query p95 budget")
		runID     = flag.String("run-id", fmt.Sprintf("r%d", time.Now().Unix()), "unique run id; devices are namespaced per run so stale rows cannot skew results")
	)
	flag.Parse()
	if *secret == "" {
		fmt.Fprintln(os.Stderr, "-jwt-secret or JWT_SIGNING_SECRET required")
		os.Exit(2)
	}
	token, err := mintToken(*secret, *tenant)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mint token:", err)
		os.Exit(2)
	}
	started := time.Now()
	fmt.Printf("loadtest: %d gateways x %.1f msg/s for %s (target %d msgs)\n",
		*gateways, *rate, *duration, int(float64(*gateways)**rate*duration.Seconds()))

	// ---- phase 1: publish ----
	var mu sync.Mutex
	markers := make([][]marker, *gateways) // per-gateway marker sends
	var wg sync.WaitGroup
	stopAt := started.Add(*duration)
	for g := 0; g < *gateways; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			serial := fmt.Sprintf("loadgen-%s-gw-%d", *runID, g)
			opts := mqtt.NewClientOptions().
				AddBroker(fmt.Sprintf("tcp://%s:%d", *mqttHost, *mqttPort)).
				SetClientID("loadtest-" + serial).SetConnectRetry(true).SetAutoReconnect(true)
			c := mqtt.NewClient(opts)
			if tok := c.Connect(); tok.Wait() && tok.Error() != nil {
				mu.Lock()
				fmt.Println("connect fail:", serial, tok.Error())
				mu.Unlock()
				return
			}
			defer c.Disconnect(250)
			topic := fmt.Sprintf("t/%s/g/%s/telemetry", *tenant, serial)
			interval := time.Duration(float64(time.Second) / *rate)
			rng := rand.New(rand.NewSource(int64(g)))
			val := 20.0
			for seq := 1; time.Now().Before(stopAt); seq++ {
				val += rng.Float64() - 0.5
				payload, _ := json.Marshal(map[string]any{
					"event_id": fmt.Sprintf("lt-%d-%d", g, seq), "device_id": "loadgen-dev-" + serial[len("loadgen-"):],
					"point_id": "temp", "observed_at": time.Now().UTC(), "value": val, "schema_version": 1,
				})
				sendAt := time.Now()
				c.Publish(topic, 1, false, payload)
				if seq%10 == 0 {
					mu.Lock()
					markers[g] = append(markers[g], marker{seq: seq, sent: sendAt})
					mu.Unlock()
				}
				time.Sleep(interval)
			}
		}(g)
	}
	wg.Wait()
	published := 0
	for g := range markers {
		// markers are 1/10 of traffic; reconstruct totals below from confirms
		_ = g
	}
	fmt.Println("publish phase done in", time.Since(started).Round(time.Second))

	// ---- phase 2: confirm ingest via the count endpoint ----
	results := make([]gwResult, *gateways)
	client := &http.Client{Timeout: 10 * time.Second}
	countURL := func(dev string, since time.Time) string {
		return fmt.Sprintf("%s/v1/telemetry/count?device_id=%s&since=%s",
			*apiBase, dev, since.UTC().Format(time.RFC3339))
	}
	getCount := func(dev string) (int, error) {
		req, _ := http.NewRequest("GET", countURL(dev, started.Add(-time.Minute)), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		var out struct {
			Count int `json:"count"`
		}
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			return 0, fmt.Errorf("count %s: %s", resp.Status, b)
		}
		return out.Count, json.Unmarshal(b, &out)
	}
	deadline := time.Now().Add(60 * time.Second)
	for g := 0; g < *gateways; g++ {
		dev := fmt.Sprintf("loadgen-dev-%s-gw-%d", *runID, g)
		sent := 0
		if len(markers[g]) > 0 {
			sent = markers[g][len(markers[g])-1].seq
		}
		results[g].sent = sent
		// wait until all sent messages are queryable, recording marker latency
		nextMarker := 0
		for time.Now().Before(deadline) {
			n, err := getCount(dev)
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			results[g].confirmed = n
			for nextMarker < len(markers[g]) && n >= markers[g][nextMarker].seq {
				results[g].latencies = append(results[g].latencies, time.Since(markers[g][nextMarker].sent))
				nextMarker++
			}
			if n >= sent {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		published += sent
	}

	// ---- phase 3: query load ----
	var qlat []time.Duration
	var qmu sync.Mutex
	qEnd := time.Now().Add(*duration / 2)
	for wkr := 0; wkr < *qWorkers; wkr++ {
		wg.Add(1)
		go func(wkr int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(wkr * 977)))
			for time.Now().Before(qEnd) {
				dev := fmt.Sprintf("loadgen-dev-%s-gw-%d", *runID, rng.Intn(*gateways))
				req, _ := http.NewRequest("GET",
					fmt.Sprintf("%s/v1/telemetry/series?device_id=%s&point_id=temp", *apiBase, dev), nil)
				req.Header.Set("Authorization", "Bearer "+token)
				t0 := time.Now()
				resp, err := client.Do(req)
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if resp.StatusCode == 200 {
						qmu.Lock()
						qlat = append(qlat, time.Since(t0))
						qmu.Unlock()
					}
				}
			}
		}(wkr)
	}
	wg.Wait()

	// ---- report ----
	var allLat []time.Duration
	lost, confirmed := 0, 0
	for _, r := range results {
		allLat = append(allLat, r.latencies...)
		confirmed += r.confirmed
		if r.confirmed < r.sent {
			lost += r.sent - r.confirmed
		}
	}
	fmt.Println()
	fmt.Printf("published=%d confirmed=%d lost=%d (%.2f%%)\n",
		published, confirmed, lost, 100*float64(lost)/float64(max(published, 1)))
	iLine, iOK := loadstats.SLOCheck("ingest publish->queryable", loadstats.Percentiles(allLat), *sloIngest)
	qLine, qOK := loadstats.SLOCheck("query series", loadstats.Percentiles(qlat), *sloQuery)
	fmt.Println(iLine)
	fmt.Println(qLine)
	ok := iOK && qOK && lost == 0
	if lost > 0 {
		fmt.Println("MESSAGE LOSS: FAIL")
	}
	if !ok {
		os.Exit(1)
	}
	fmt.Println("all SLOs met")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
