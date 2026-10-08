# Assistant runs: durable record and replay

Every assistant chat run is stored (table `assistant_runs`): id, tenant, user, status (`running`, `done`, `failed`, `interrupted`), the stored answer and times.

- Send `X-Run-Id` (8 to 64 letters, digits or dashes) on `POST /v1/assistant/chat`. If a connection drops and you send the same id again, you get the stored answer back (header `X-Run-Replayed: true`) and the model is not called again. Without the header the server makes an id and returns it as `run_id`.
- Same id while the first run is still going, or after a failure: HTTP 409 with the status. The model is not run again; send a new id to ask again.
- `GET /v1/assistant/runs/{id}` shows your own run: status, and the stored result when it finished. Other users and other tenants get 404.
- If the server stops mid-run, that run becomes `interrupted` at the next start. A run still marked running after 15 minutes reads as interrupted.

## What this is not

- The model loop is not resumed after a crash. A half-finished loop cannot be continued safely, so you ask again.
- Nothing is executed on replay. Changes still wait for the user's confirmation; what happened to each action is in the action outcome record (see assistant-action-outcomes.md).
- Streaming chats (`?stream=1`) are recorded and marked finished, but their answer is not stored and they cannot be replayed.
- The startup sweep assumes one server process (single node). With several processes sharing a database, one restart would mark the others' live runs interrupted.
