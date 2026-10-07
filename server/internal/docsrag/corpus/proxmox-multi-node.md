# One container or many, and a 3-4 server Proxmox setup

Written 2026-10-07 for the HexThings stack. Two kinds of statements below: **Tested** (verified in this repo, on a single dev VM) and **Recommendation** (architecture advice, never run on a Proxmox cluster).

## Short answer

Use separate containers per service. Do not put everything in one container.
Reasons grounded in this stack:
- The repo already ships it that way: `docker-compose.yml` has postgres, mosquitto, api, ingest, mcp, elasticsearch (optional), ai-runtime (profile `ai`), redis (profile `ha`), web. (Tested: compose smoke in earlier units, single host.)
- The services scale differently. API is stateless and scales out; ingest scales with MQTT shared subscriptions (`INGEST_SHARED_GROUP`, opt-in); Postgres and the broker are stateful and scale by other means; the AI runtime needs about 3 GB and should not compete with the database for RAM.
- A crash or upgrade of one part (e.g. the model runtime) must not take down telemetry ingest.
- One container for all would need a process manager inside it, shared logs and one restart unit. Not recommended.
A single VM running the compose file is the right start for a pilot or a small site.

## What is and is not proven

| Item | Status |
|---|---|
| Compose stack on one host | Tested (earlier units, single VM) |
| Stateless API, leader-elected scheduler (Postgres advisory lock, failover) | Tested with competing contenders in unit tests |
| Redis shared cache and sessions | Tested against a fake Redis only |
| Shared MQTT subscriptions for ingest replicas | Unit-tested topic wiring; not load-tested with several replicas |
| Ingest throughput | About 3,000 readings/s on one API process, dev VM (docs/scaling.md) |
| Everything on Proxmox, LXC/VM choice, HA failover, autoscaling | Not tested. Recommendation only |
| Postgres HA, broker clustering | Not shipped; deployment-level work |

## Recommended layout for 3 to 4 Proxmox nodes

Use VMs (not LXC) for anything stateful or running Docker, so kernel and storage behaviour is predictable. Run Docker or Podman inside each VM.

**Node 1 - data (Postgres primary VM).** Postgres with local SSD or fast shared storage. Take WAL archiving or `pg_dump` backups to a different node or NAS (docs/backup-restore.md).
**Node 2 - data replica + broker.** Postgres streaming replica (manual promote at first; use Patroni only if you have someone to operate it) and a second broker role if you cluster the broker.
**Node 3 - app VM.** api x2, ingest x2, mcp, web, redis. These are stateless, so they can move to any node.
**Node 4 (optional) - AI and search VM.** ai-runtime (3 GB limit) and Elasticsearch if you use it. Keeping it separate protects the database from memory pressure.
With only 3 nodes, fold node 4 into node 3 and keep the AI runtime memory-limited.

Front the app VMs with a TLS reverse proxy (nginx, Traefik or HAProxy), ideally two with a floating IP (keepalived). Expose only 443 and 8883; Postgres and plain 1883 stay internal (docs/deployment.md).

## Proxmox-level settings

- Enable Proxmox HA for the Postgres and app VMs only if their disks are on shared or replicated storage (Ceph, or ZFS replication with accepted data-loss window). Otherwise HA restarts a VM on a node that lacks its disk.
- Ceph needs 3 nodes with several disks each; do not enable it on thin hardware.
- Use anti-affinity (HA rules) so the primary and replica Postgres VMs never share a node.
- Back up VMs with Proxmox Backup Server in addition to database-level backups. VM snapshots alone are not a consistent database backup.
- Set the VM memory fixed (no ballooning) for Postgres and the AI runtime.

## Scaling and autoscaling honestly

- Scale app replicas by hand first (`docker compose up --scale api=2`) and measure. Real autoscaling needs a metrics source and an orchestrator (Kubernetes or Nomad); Proxmox does not autoscale containers by itself. Not built, not tested.
- Per-replica limits to know (docs/scaling.md): API-key rate limit is per replica, so N replicas multiply it by N; the Postgres pool is per replica (`DB_MAX_CONNS`), so size `max_connections` for replicas x pool, or add PgBouncer.
- The first real load test on multiple nodes is still owed; do not promise throughput before it.

## Suggested rollout order

1. One VM with compose, run `api -check-config`, run the restore drill.
2. Split Postgres to its own VM; add backups to another node.
3. Add a second api and ingest replica; load-test with `cmd/loadtest` and record numbers.
4. Add the Postgres replica and a documented manual failover drill.
5. Only then consider Kubernetes/Nomad for autoscaling.
