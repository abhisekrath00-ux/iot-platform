# Server addresses for edge devices

Edge installs need an address that the edge box can reach. HexThings never uses the browser address for this.

## Where the address comes from
In order: addresses set for the install's site (Settings > Server addresses), then workspace-wide addresses, then the `API_PUBLIC_URL` environment variable. Lowest priority number first. The first is the primary, the rest are listed as fallbacks.

Use a site address when a site sits on a different network, region or behind NAT. Behind a load balancer or HA pair, use its public name. Settings shows suggestions taken from the server's network interfaces; pick the one the edge boxes can reach.

## Safety checks
- Only `http(s)://host[:port]` is accepted (no paths, credentials or queries).
- Localhost, 127.x, ::1 and 0.0.0.0 are accepted but flagged. The Edge setup page and the claim-code response warn when the primary address is loopback or when nothing is configured.
- "Test" asks `<address>/healthz` from the server itself. It proves the server can reach it, not that an edge network can. Link-local and unspecified addresses are not probed.
- Changes are admin-only and audited (`server.endpoint.add`, `server.endpoint.delete`).

## API (admin)
`GET /v1/system/endpoints?site=`, `POST /v1/system/endpoints`, `DELETE /v1/system/endpoints/{id}`, `POST /v1/system/endpoints/check`. The claim-code response (`POST /v1/enrollment/tokens`) includes `server_url`, `fallback_urls`, `address_source` and `warnings`.

## Not built yet
- The edge agent takes one address; it does not yet try fallbacks.
- LAN auto-discovery (mDNS) for installs is not wired into the installer.
- Reachability from the edge's own network can only be proven by the edge itself.
