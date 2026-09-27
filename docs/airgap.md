# Air-gapped deployment

The platform runs fully offline. Every runtime dependency ships inside the
bundle; no image pull, package download, or external API call is required at
install or run time.

## What needs internet, and the offline answer

| Component | Online default | Air-gapped answer |
|---|---|---|
| Container images | Docker Hub / docker.elastic.co pulls | Pre-built `images.tar.gz` in the bundle |
| Go/Node toolchains | pulled at `docker compose build` | Build happens on the bundling machine; the target never builds |
| Slack alerts | slack.com webhook | Point `SLACK_WEBHOOK_URL` at an internal relay (e.g. Mattermost incoming webhook or a mail-to-chat bridge), or disable and use SMTP only |
| Email alerts | customer SMTP | Works as-is against the enterprise's internal mail server (`SMTP_HOST` etc.) |
| OIDC/SSO | public IdP | Point `OIDC_ISSUER` at an internal IdP (Keycloak/Entra on-prem). Discovery and JWKS fetches go to that issuer only. If unset, SSO routes return 404 and local accounts are used |
| Time | public NTP | Gateways and server must sync to the enterprise NTP; certificate and JWT expiry checks depend on correct clocks |

There are no other external runtime calls. Search is the bundled
Elasticsearch; the broker is the bundled Mosquitto; the database is the
bundled Postgres.

## Build the bundle (on a connected machine)

```sh
./scripts/airgap-bundle.sh
# produces dist/airgap/: images.tar.gz, docker-compose.yml, env.template, SHA256SUMS
```

## Install (on the air-gapped host)

```sh
sha256sum -c SHA256SUMS
gunzip -c images.tar.gz | docker load
cp env.template .env   # fill secrets; see docs/setup.md
docker compose up -d
docker compose ps      # all services healthy
```

## Upgrade

1. Build a new bundle at the target version on a connected machine.
2. Copy it in, `docker load`, then `docker compose up -d`.
3. The api service applies SQL migrations at startup; they are additive and
   idempotent, so rolling back is `docker load` of the previous bundle plus
   `up -d`. Database downgrades are not automatic - snapshot `pgdata` first.

## Gateway images

`hexmon/edge-agent:latest` is included in the same tarball for loading onto
gateway SBCs. Gateways enroll with a one-time claim code against the local
API; no outbound internet is needed at any point.
