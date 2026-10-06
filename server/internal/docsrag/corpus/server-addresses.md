# Server addresses for edge devices

Edge installs need an address that the edge box can reach. HexThings never uses the browser address for this.

## Where the address comes from
In order: addresses set for the install's site (Settings > Server addresses), then workspace-wide addresses, then the `API_PUBLIC_URL` environment variable. Lowest priority number first. The first is the primary, the rest are listed as fallbacks.

Use a site address when a site sits on a different network, region or behind NAT. Behind a load balancer or HA pair, use its public name. Settings shows suggestions taken from the server's network interfaces; pick the one the edge boxes can reach.

## Fallbacks at install time
The wizard's commands pass `primary,fallback1,...` to the installer. `install.sh` and `install.ps1` check each address in order, use the first that answers, warn about loopback addresses, and fail if none answer. The claim step (`edge-agent -claim-api a,b` or an enroll string with fallbacks) tries addresses in order, moving on only when one cannot be reached or answers 5xx. A refused code is never retried elsewhere. Tested: Go tests for claim fallback and enroll strings, a shell test with a fake curl (`scripts/test-edge-install.sh`). The Windows installer change is parse-checked only.

## Safety checks
- Only `http(s)://host[:port]` is accepted (no paths, credentials or queries).
- Localhost, 127.x, ::1 and 0.0.0.0 are accepted but flagged. The Edge setup page and the claim-code response warn when the primary address is loopback or when nothing is configured.
- "Test" asks `<address>/healthz` from the server itself. It proves the server can reach it, not that an edge network can. Link-local and unspecified addresses are not probed.
- Changes are admin-only and audited (`server.endpoint.add`, `server.endpoint.delete`).

## API (admin)
`GET /v1/system/endpoints?site=`, `POST /v1/system/endpoints`, `DELETE /v1/system/endpoints/{id}`, `POST /v1/system/endpoints/check`. The claim-code response (`POST /v1/enrollment/tokens`) includes `server_url`, `fallback_urls`, `address_source` and `warnings`.

## Not built yet
- Fallbacks are used at install and claim time only. After enrollment the agent uses the broker address it was given; it does not switch servers at runtime.
- LAN auto-discovery (mDNS): the edge agent has a discover mode of its own, but the installer does not call it yet.
- Reachability from the edge's own network can only be proven by the edge itself.
