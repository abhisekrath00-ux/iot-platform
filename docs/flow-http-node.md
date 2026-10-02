# Flow HTTP request node

Calls an HTTP(S) endpoint from inside a flow and puts the answer into a variable, so later nodes can branch on it
(for example "only alarm if the outside temperature from the weather service is above 35").

- Method GET or POST. The URL is fixed text: no credentials in it, no placeholders, so reading data cannot steer the
  request to another host. POST bodies are templates (`{"v":{value}}`) sent as `application/json`.
- The answer: with an **extract** path (`main.temp`, array indexes as numbers) the number, string or boolean at that path;
  with no path the trimmed body. Values over 256 bytes are rejected. The value goes to `vars.<target>`, the status code
  to `vars.<target>_status`.
- Two outputs: 1 = 2xx and a usable value, 2 = anything else (error, timeout after 5 s, non-2xx, bad JSON, missing path,
  per-run limit of 2 requests, feature off).
- Safety: off by default per tenant. An admin enables `http_nodes` (`PUT /v1/features/http_nodes {"enabled":true}`), and only
  an admin can save or import a flow containing the node. Requests use the webhook client: loopback, link-local (incl.
  the cloud metadata address), unspecified and multicast addresses are refused when the connection is made (so DNS
  rebinding does not help), redirects are not followed, responses are capped at 16 KiB. Private networks (RFC 1918)
  are reachable on purpose, for on-prem systems.
- Dry runs (the editor's Test button) never send the request; the node takes output 2 and the debug line says so.

Not built: custom request headers and authentication (needs a place to keep secrets; use a gateway or proxy that
adds them), PUT/DELETE, response headers, Node-RED import/export mapping for `http request`, a toggle for the
feature in the UI (API only for now). Requests run inline while the reading is processed, so a slow endpoint delays
that flow's evaluation by up to 5 s per call; combine with a rate-limit node on busy points.

Tested with a fake HTTP client (routing, extraction, failures, per-run cap, validation), the SSRF-safe client against a
local server, and an API test for the feature gate and dry run. Not tested against real third-party services.
