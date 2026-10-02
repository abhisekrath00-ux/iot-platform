# Deployment guide (cloud & on-prem)

The same container images deploy to Hexmon cloud and to customer premises.
On-prem must run fully without the vendor cloud.

## Topology

- Single VM (small on-prem / pilot): Docker Compose (this repo's compose file,
  production overrides below).
- Larger / HA: Kubernetes (charts land in `deploy/k8s` when ops capacity exists;
  do not start here).

## Production checklist

1. **TLS everywhere.** MQTT listener 8883 with mTLS: CA + server certs on the
   broker, unique client certificate per gateway. Uncomment the production
   sketch in `deploy/mosquitto/mosquitto.conf` and mount `certs/`.
   Web/API behind a reverse proxy (nginx/traefik) terminating TLS 1.2+.
2. **Broker ACLs.** Per-gateway ACL: publish only
   `t/{tenant}/g/{gateway}/telemetry`, subscribe only
   `t/{tenant}/g/{gateway}/cmd`. No wildcards across tenants.
3. **Secrets.** All secrets from environment or a secrets manager. Never in
   images, never in git. Rotate `JWT_SIGNING_SECRET` and DB creds on schedule.
   `SECRETS_KEY` (base64, 32 bytes) enables the per-tenant encrypted secrets store; back it up separately from the database, since without it stored secrets cannot be read.
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

## Cloud (Hexmon-operated)

Compose on a hardened VM is acceptable for pilot. Restrict inbound ports to
443 and 8883; Postgres and 1883 must never be internet-exposed.

## On-prem (customer-operated)

Ship: compose file, images (or an offline bundle), this guide, and the systemd
units below. The stack must boot and serve with no internet access; outbound
update/support channels are optional and off by default.

### Edge gateway systemd unit (`/etc/systemd/system/hexmon-edge.service`)

```ini
[Unit]
Description=Hexmon edge agent
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
