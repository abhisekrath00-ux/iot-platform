# Deployment guide (cloud & on-prem)

The same container images deploy to HexThings cloud and to customer premises.
On-prem must run fully without the vendor cloud.

## Topology

- Single VM (small on-prem / pilot): Docker Compose (this repo's compose file,
  production overrides below).
- Larger / HA: Kubernetes (charts land in `deploy/k8s` when ops capacity exists;
  do not start here).

## Production checklist

1. **TLS everywhere.** MQTT listener 8883 with mTLS: CA + server certs on the
   broker, unique client certificate per gateway. Run `scripts/gen-ca.sh` to
   create the device CA and set `MTLS_CA_CERT`/`MTLS_CA_KEY` in `.env` so the
   API can sign gateway certificates at enrollment - without them, gateway
   claims fail closed (only the unauthenticated lab posture `MQTT_TLS=false`
   gets a token-only identity). Uncomment the production
   sketch in `deploy/mosquitto/mosquitto.conf` and mount `certs/`.
   Web/API behind a reverse proxy (nginx/traefik) terminating TLS 1.2+.
2. **Broker ACLs.** Per-gateway ACL: publish only
   `t/{tenant}/g/{gateway}/telemetry`, subscribe only
   `t/{tenant}/g/{gateway}/cmd`. No wildcards across tenants.
3. **Secrets.** All secrets from environment or a secrets manager. Never in
   images, never in git. Rotate `JWT_SIGNING_SECRET` and DB creds on schedule.
   `SECRETS_KEY` (base64, 32 bytes) enables the per-tenant encrypted secrets store; back it up separately from the database, since without it stored secrets cannot be read.
   `DB_MAX_CONNS` sets the per-replica DB pool (default max(20, 4 x CPUs)); size Postgres `max_connections` for replicas x pool.
   Run `api -check-config` (see pilot-readiness.md) to catch weak or placeholder secrets; `STRICT_CONFIG=1` makes the API refuse to start on a FAIL.
4. **Database.** Automated `pg_dump` (or WAL archiving) with a tested restore
   drill; document RPO/RTO with the customer. Telemetry partitions by month —
   see migrations; set retention per contract.
5. **Updates.** Signed images; pin digests in production compose. Edge agent
   updates are staged by cohort with health checks and rollback (TUF-style
   metadata when the fleet grows).
6. **Observability.** `/healthz` on every service; structured logs to stdout;
   metrics endpoint before GA. Alarm on ingest lag, queue depth, stale fleet.
7. **Backups & restore drill** before pilot go-live. A backup that was never
   restored is not a backup.

## Cloud (HexThings-operated)

Compose on a hardened VM is acceptable for pilot. Restrict inbound ports to
443 and 8883; Postgres and 1883 must never be internet-exposed.

## On-prem (customer-operated)

Ship: compose file, images (or an offline bundle), this guide, and the systemd
units below. The stack must boot and serve with no internet access; outbound
update/support channels are optional and off by default.

### Edge gateway systemd unit (`/etc/systemd/system/hexmon-edge.service`)

```ini
[Unit]
Description=HexThings edge agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/edge-agent --config /etc/hexmon/edge-agent.yaml
Restart=always
RestartSec=5
User=hexmon
# hardening
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/hexmon
# serial access
SupplementaryGroups=dialout

[Install]
WantedBy=multi-user.target
```

### Upgrades

1. Announce maintenance window. 2. Pull new images, run migrations automatically
at API start (idempotent). 3. Verify `/healthz` and one live telemetry point.
4. Roll back by re-deploying the previous image digest; migrations are written
to be backward compatible within a release pair.


## Local MQTT pipeline regression

`TestIntegrationMQTTPipeline` in cmd/api starts the compiled ingest process, publishes through a loopback broker and checks real Postgres plus JWT-authenticated latest/series HTTP handlers. Run from server with a disposable migrated/test database, `TEST_DATABASE_URL`, `TEST_MQTT_PORT` (broker on127.0.0.1) and `TEST_INGEST_BINARY` (build `go build -o /tmp/ingest-pipeline ./cmd/ingest`). The test requires both optional variables or skips. CI's dedicated step starts a disposable anonymous Mosquitto container bound to127.0.0.1 and runs this test explicitly.

This is synthetic lab transport-to-API evidence. It checks QoS1 publication, same(event_id,observed_at) duplicate suppression, payload-tenant spoof rejection, latest/series reads, cross-tenant empty data and missing-JWT401. It does not check broker-authenticated identity, mTLS/ACLs, broker restart/persistence, offline replay, physical gateway polling, chart pixels or Windows/arm64 execution. Never use the anonymous test broker as production config. The database must be disposable: the integration seed creates named synthetic tenants/devices and deletes their telemetry. No notification rules or external destinations are configured by the test.

### Ingest subscription recovery

Ingest registers telemetry, commissioning result and ACK subscriptions before its first connection, then installs them again on every MQTT connection. This matters with clean sessions: a new connection alone does not restore broker subscriptions. Each SUBACK has a 10-second wait; broker rejection or timeout stops the process rather than logging readiness with missing topics. An external supervisor must restart a failed process. The optional Sparkplug bridge registers its six filters through the same reconnect path.

The opt-in synthetic MQTT pipeline test cuts the ingest TCP connection twice via a local proxy, waits for subscription installation after each reconnect, then publishes telemetry and checks Postgres plus authenticated latest/series HTTP handlers. It uses the real ingest executable and an anonymous loopback test broker. Unit tests check accepted/refused SUBACKs, timeout, error and cancellation. This is not a durable offline queue, broker restart test, crash replay test, mTLS/ACL validation or a guarantee about data sent while disconnected. Sparkplug BIRTH/alias state and command/commissioning round trips after reconnect still need separate transport tests; registration coverage does not prove their full workflows.

### Manual scan request serialization

Manual scan requests reserve a database row under a tenant-owned gateway row lock, committing before the broker publish. Concurrent tabs/replicas see the same recent requested-scan gate, instead of racing a count and insert. Only one recent manual request is accepted per gateway. Completed/failed scans and requests older than ten minutes release this gate. Database errors fail closed. The UI blocks duplicate submits while requesting, ignores late errors from a different gateway, and requires a successful explicit Refresh results after an uncertain response before retrying.

This is not an exactly-once MQTT delivery guarantee. A crash after reservation but before publish can leave a requested scan until timeout. A broker timeout can be ambiguous. Automatic discovery proposals do not acquire this manual-reservation lock. The ten-minute cutoff does not cancel a slow physical scan; an old operation may still run at the gateway. Tests use concurrent Postgres reservations and synthetic UI responses, not physical scans.

### Post-reconnect scan results and command ACK evidence

The opt-in MQTT pipeline regression also seeds synthetic manual scan and command records, then sends inbound replies through the real broker after two ingest TCP reconnects. It checks topic tenant/gateway mismatch rejection; a matching scan completion visible through the JWT scan-history handler; a sent command ACK and rejected-to-failed outcome; an ACK cannot settle a pending-approval command; and conflicting late replies cannot overwrite a terminal scan/command outcome. Cross-tenant scan/command reads do not expose these records.

No outbound scan/command is dispatched and no actuator or physical gateway is involved. This proves the inbound scan/result and cmd/ack transport/state paths in the lab only. It does not prove approval/dispatch, actuation, physical commissioning, other ACK families, broker-authenticated identity, mTLS/ACLs, offline replay or broker restarts. The anonymous local broker is test-only.


### Server service MQTT TLS transport

The ingest subscriber and API publisher now share `MQTT_TLS`, `MQTT_HOST`, `MQTT_PORT`, `MQTT_CA_FILE`, `MQTT_CERT_FILE`, `MQTT_KEY_FILE` transport configuration. `MQTT_TLS=true` requires all three readable PEM files and a valid client certificate/key pair, uses TLS1.2 or later and verifies broker trust and hostname. Bad TLS inputs fail rather than fall back to plaintext. TLS defaults to port8883; existing unset/false lab mode defaults to1883. Certificate settings in plaintext mode are refused to catch accidental downgrade. Host is a hostname or bare IP (including IPv6), not a URL or host:port.

Use separate service certificates, CN `ingest` and `api`, for the certificate-required broker listener and its generated ACL header. Do not reuse gateway keys or mount the CA signing key into these service clients. Mount read-only certificate files and set the variables for each service in a deployment-specific compose override. The supplied compose/HA install remains an explicitly plaintext lab profile: it does not mount these files or enable production TLS automatically. Existing edge TLS configuration is separate. No credentials or keys ship in the repository.

`TestLocalMTLSTransport` uses a disposable real Mosquitto and ephemeral synthetic CA/service/gateway certificates. It checks trusted service subscription, allowed gateway telemetry, a foreign-tenant publish not delivered, and refusal of missing client certificate, wrong broker hostname and untrusted server CA. Run from server with `TEST_MOSQUITTO_BINARY=/path/to/mosquitto go test ./internal/mqttconfig -run TestLocalMTLSTransport -count=1 -v`. CI runs this explicitly. Unit tests cover lab compatibility, IPv6, invalid flag/host/port, missing/malformed/mismatched TLS files. This transport test is not a full compiled-ingest TLS-to-database test, certificate enrollment/rotation/revocation proof, full production ACL audit, broker restart test or physical-device acceptance.


### Compiled ingest TLS-to-API regression

`TestIntegrationMQTTTLSPipeline` now runs the compiled ingest binary against a disposable certificate-required Mosquitto with a synthetic CA, broker cert, ingest CN and gateway CN. The gateway ACL permits only its own telemetry, scan/result and cmd/ack topics. After two TCP cuts force real TLS reconnects and subscription recovery, the test checks telemetry persistence, duplicate suppression, payload-tenant spoof refusal, rejected foreign-topic telemetry, seeded inbound scan/command state changes and JWT-scoped latest/series/history reads. Foreign-tenant telemetry is absent after a same-connection permitted barrier event lands. No outbound command, scan or notification is sent.

Run from server with a disposable `TEST_DATABASE_URL`, `TEST_MOSQUITTO_BINARY` and freshly built `TEST_INGEST_BINARY`, using `go test ./cmd/api -run '^TestIntegrationMQTTTLSPipeline$' -count=1 -v`. CI runs this separately from the anonymous plaintext test and the TLS handshake-rejection test. The harness uses a narrow synthetic ACL rather than the complete generated production ACL; it does not prove certificate enrollment, renewal/revocation, API publish workflows, broker restarts/persistence, offline replay, actual edge polling or physical-device acceptance. The test generates ephemeral keys locally and removes them with its temporary directory.
