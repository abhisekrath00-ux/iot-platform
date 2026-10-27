# Broker per-gateway ACLs

The production broker (mTLS, `deploy/mosquitto/mosquitto-mtls.conf`) authenticates
gateways by client certificate; `use_identity_as_username true` makes the
certificate CN (= gateway serial) the broker username. The ACL file then
pins each gateway to its own topic subtree:

```
user AXON-0007
topic write t/acme/g/<gateway-uuid>/telemetry
topic write t/acme/g/<gateway-uuid>/diag/result
topic write t/acme/g/<gateway-uuid>/cmd/ack
topic write t/acme/g/<gateway-uuid>/fleet/ack
topic read  t/acme/g/<gateway-uuid>/fleet
topic read  t/acme/g/<gateway-uuid>/cmd
topic read  t/acme/g/<gateway-uuid>/diag
```

A compromised gateway can publish only as itself and read only its own
command/diag topics. It cannot subscribe to other tenants, other gateways,
or the diag channel of a neighbor.

## Lifecycle

- **Generated at claim**: when `POST /v1/enrollment/claim` issues a
  certificate, the API regenerates the file from every active, cert-holding
  gateway (deterministic, sorted, atomic tmp+rename write).
- **Manual regeneration**: `POST /v1/broker/acl/regenerate` (admin) - use it
  after decommissioning a gateway so its block disappears. Decommissioning =
  set the gateway row inactive/revoked, regenerate, reload.
- **Enable**: set `BROKER_ACL_FILE=/mosquitto/config/acl` on the api service
  and `acl_file /mosquitto/config/acl` in `mosquitto-mtls.conf`, mount the
  same directory into both containers.
- **Reload**: mosquitto rereads the file on SIGHUP:
  `docker compose kill -s HUP mosquitto`. Until reload, the previous ACL
  stays active - a newly claimed gateway can publish after at most one
  reload; a revoked one loses access on reload (and its certificate is
  rejected at the TLS layer once the CRL/rotation lands).

Service users (`ingest` read-all, `api` diagnostic/scan publisher) live in the file's
static header. On the certificate-required production listener they need distinct
client-auth certificates with CN `ingest` and `api`, signed by the broker-trusted CA.
Password-file credentials alone do not satisfy `require_certificate true` and
`use_identity_as_username true`. Gateway blocks never get wildcards. The static
API header is not a blanket command/fleet dispatch grant; verify each enabled
workflow's ACL before use. Air-gap: everything is local file + SIGHUP, no plugin or network
service required.
