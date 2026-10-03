# Live subflows (by-reference fragments)

A flow draft can contain a **subflow** node that points at a saved fragment. Status: built and tested against a real Postgres; not remote-CI verified; UI checked by type-check only.

How it works
- Saving a draft (or creating a flow, testing, simulating) expands each subflow node into the fragment's nodes. Their ids are prefixed with the subflow node's id. Edges into the subflow go to the fragment's entry; edges out of it leave from every exit node.
- The stored definition keeps both: `source` (the editable graph with subflow nodes, versions pinned) and `graph` (the expansion that runs). The engine only reads `graph`, so there is no new runtime behaviour.
- Fragments are versioned. A subflow pins a version when the draft is saved (no version means the current one).

Safety rules
- Editing a fragment (`PUT /v1/flow-fragments/{id}`) creates a new version. No stored or published flow changes.
- `GET /v1/flow-fragments/{id}/usage` lists flows using it and whether they are behind. `POST /v1/flows/{id}/refresh-subflows` makes a new draft on the current fragment versions. It never publishes; publishing is the normal step.
- A fragment cannot contain a subflow (depth one, so no cycles). At most 10 subflow nodes per flow; the expanded graph must still fit the 50-node limit.
- A fragment in use cannot be deleted (409). Fragments are tenant-scoped; another tenant's id is "fragment not found".
- Function, HTTP and control nodes in a fragment keep their admin and feature gates, applied to the expanded graph on save and publish.
- The client-supplied `source` is ignored and rebuilt by the server.

Not built: nested subflows, parameters or per-instance overrides, multiple entry or named exits, automatic upgrade of published flows (deliberately manual).
