# Multi-node high availability (3 nodes)

Single node: `scripts/install.sh` (already built, unchanged, see docs/install.md). Multi node: `scripts/install-ha.sh` (this page). One entry point offers both: `bash scripts/setup.sh` shows a menu (1 single node, 2 multi node), or `bash scripts/setup.sh single|multi [args]`. The menu itself is tested only for the non-interactive paths; the interactive prompt has not been exercised on a terminal.

**Status, stated plainly.** The generator is tested (`scripts/test-install-ha.sh`: input validation, rendering, YAML structure, secrets handling, file modes). The rendered stack (etcd, Patroni, HAProxy, keepalived, the Patroni image) has **never been started**: there is no Docker and no second machine in the build environment. Every failover claim below is a design, not a result. The drill at the end must pass on real hardware before you rely on it. The generator's own test caught two real bugs in the rendered files (an unquoted etcd host list and an invalid volume list), which is a sign the rendered files likely still hold more.

## What you get

Three nodes and one virtual IP (VIP). Every node runs the same set:

| Part | Mode | Failover behaviour (design) |
|---|---|---|
| etcd | 3-member cluster | Holds the "who is the Postgres leader" lease. Needs 2 of 3 up. |
| Patroni + Postgres 16 | 1 leader, 2 replicas, synchronous replication | Leader lease expires (about 30 s), a replica that has all committed data is promoted, the old leader is rewound with pg_rewind when it returns. |
| HAProxy | on every node | Port 5000 always goes to the node Patroni says is primary. Web 8080 and API 8000 are load-balanced across all nodes (active-active). MQTT 1883 goes to one broker, the others are backups (active-passive). |
| keepalived | VIP floats between nodes | The VIP moves to another node if HAProxy or the node dies. |
| api, ingest, mcp, web | active-active on all 3 | Stateless. Ingest replicas share the MQTT load with shared subscriptions (`INGEST_SHARED_GROUP`). The scheduler runs on one replica at a time (Postgres advisory lock). |
| redis | one per node, not replicated | Cache and login/OIDC state. Losing it can sign users out; it holds no source-of-truth data. |
| mosquitto | one per node, session files not replicated | See the MQTT limits below. |

Clients use: web `http://VIP:8080`, API `http://VIP:8000`, MQTT `VIP:1883`, Postgres `VIP:5000`.

## Commands

Run the scripts with `bash` (files published through the GitHub web editor lose the executable bit; `chmod +x scripts/*.sh` also works).

```
bash scripts/install-ha.sh plan   --nodes n1=10.0.0.11,n2=10.0.0.12,n3=10.0.0.13 --vip 10.0.0.10
bash scripts/install-ha.sh render --nodes n1=...,n2=...,n3=... --vip 10.0.0.10 --iface eth0 [--sync-strict]
# copy ha-out/<node> to each server (inside a HexThings checkout), then on each:
docker compose up -d --build
bash scripts/install-ha.sh check  --nodes ... --vip ...
bash scripts/install-ha.sh drill  --vip 10.0.0.10
```
Render refuses a non-empty output folder, generates fresh secrets (shared by the three nodes, `.env` mode 600) and never writes a secret into the compose, Patroni or HAProxy files. Air-gapped sites: build the Patroni image on a connected machine and carry it with scripts/airgap-bundle.sh (the bundle does not yet include the HA images: not built).

## Real tradeoffs

**Data loss vs availability (the main choice).**
- Default (synchronous replication, not strict): a committed write is on the leader and one replica. If one node dies, no committed write is lost. If both replicas are down, Patroni falls back to asynchronous so the system keeps running, and during that window a leader failure can lose recent writes.
- `--sync-strict`: writes stop (the database becomes read-only for writers) rather than ever run without a synchronous replica. Zero data loss, lower availability. Pick this if losing a write is worse than a short outage.
- Cost of synchronous replication: each commit waits for a replica, so write latency rises by one network round trip and ingest throughput drops. Not measured here.

**Split brain.** Only the node holding the etcd lease may be primary, and etcd needs a majority, so a node cut off from the other two demotes itself. This is why exactly 3 nodes are required: two nodes cannot tell a dead peer from a broken link, so they cannot fail over safely without a third voter. Two-node setups are not offered. Losing two nodes stops writes by design. Add a hardware or Proxmox watchdog for fencing a hung node: recommended, not configured here. The VIP can briefly sit on two nodes during a network partition; the database stays safe because HAProxy only sends port 5000 traffic to the Patroni primary.

**MQTT is the weak spot.** Brokers are not clustered. After a broker failover, persistent sessions and queued QoS 1/2 messages for offline subscribers on the old broker are lost. Telemetry from HexThings edge agents is protected by their local store-and-forward buffer (the agent reconnects and resends), but third-party publishers without a buffer can lose messages in that window. A real broker cluster (EMQX or similar) is the fix; it is not built.

**Not covered.** Elasticsearch/search HA, the optional AI runtime, TLS between nodes and to etcd, etcd authentication, off-site backups (use docs/backup-restore.md and Patroni's WAL archiving, not configured), and rolling-upgrade procedure. Stateful volumes live on each node's local disk.

## Proxmox notes

Run each node as a VM on a different Proxmox host. Do not also enable Proxmox HA on these VMs: Patroni already handles database failover and double management causes fights. Keep the three VMs on different physical hosts (anti-affinity) and put the VM disks on local fast storage, not a single shared disk.

## Failover drill (must be run on real hardware)

`bash scripts/install-ha.sh drill --vip ...` prints the steps: write a marker through the VIP, stop the leader (then the harder test: power the VM off), expect a new leader in about 30 to 45 seconds, read the marker back, restart the old node and confirm it rejoins as a replica, then record the measured time and any lost write here. Until a result is recorded below, this feature is **untested end to end**.

| Date | Hardware | Failover time | Lost writes | Result |
|---|---|---|---|---|
| (not yet run) | | | | |
