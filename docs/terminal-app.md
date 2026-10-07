# Terminal app (`scripts/setup.sh`)

`bash scripts/setup.sh` opens an interactive menu with the animated HexThings wordmark. Each action also works as a command, so it can be scripted.

| Menu item / command | What it does | Test status |
|---|---|---|
| Install single node / `single [args]` | Hands over to `scripts/install.sh` unchanged (its own animation and guided steps) | Hand-over tested; the installer itself is as before |
| Install multi node / `multi ...` | Guided prompts (3 nodes, virtual IP, interface), then the HA generator `install-ha.sh` | Prompts not exercised on a terminal; generator tested (docs/ha-multi-node.md) |
| Status / `status` | Services with state and health, up-count bar | Tested against a stubbed `docker` |
| Health check / `health` | Probes API, web, MQTT port and Postgres, with latency; exit 1 if anything is down | Tested against a stubbed `curl`, a local test socket and stubbed docker |
| Follow logs / `logs [service]` | `docker compose logs -f`; Ctrl-C stops following and returns to the menu | Ctrl-C behaviour tested with a stub |
| Backup now / `backup` | Runs `scripts/backup.sh` | Delegates to the existing script; not re-tested here |
| Restore / `verify FILE` / `restore FILE` | Verifies first, then needs you to type RESTORE, stops api and ingest, restores, starts them again | Wrong-confirmation path tested; a real restore not run from the app |
| Upgrade / `upgrade` | Runs `scripts/upgrade.sh` (backup, update, health check, auto-rollback) | Delegates; not re-tested here |
| Cluster view / `cluster [host]` | Patroni members: leader, sync standby, replicas, state, lag | Tested against a stubbed Patroni reply; never against a real cluster |

Robustness, tested through a real pseudo-terminal (`python3 scripts/test-setup-ui.py`, 30 checks): arrow keys, j/k, number keys, Enter, q; unknown keys ignored; Ctrl-C exits with 130 and restores the cursor; narrow terminal (40 columns) falls back to a plain wordmark and clipped lines (this caught a real overflow bug); NO_COLOR gives a numbered plain menu with bad input rejected; no terminal means subcommands only (menu refuses, exit 2).

Not tested: a real Docker or HA cluster, Windows (use `scripts/hexthings.ps1` there), terminals other than xterm-256color through the pseudo-terminal, screen readers. `scripts/hx-ui.sh` is a copy of the wordmark code in `install.sh`; a test fails if they drift apart.
