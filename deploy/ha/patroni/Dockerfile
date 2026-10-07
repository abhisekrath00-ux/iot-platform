# Postgres 16 with Patroni (etcd3 backend) for automatic failover. Build on a connected machine and
# carry the image to air-gapped sites with scripts/airgap-bundle.sh. Never built or run in this repo's
# test environment (no Docker there): treat as untested until the failover drill in docs/ha-multi-node.md passes.
FROM postgres:16-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends python3 python3-pip python3-venv curl \
    && python3 -m venv /opt/patroni && /opt/patroni/bin/pip install --no-cache-dir "patroni[etcd3]==3.3.2" psycopg2-binary \
    && apt-get purge -y python3-pip && apt-get autoremove -y && rm -rf /var/lib/apt/lists/*
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
USER postgres
ENTRYPOINT ["/entrypoint.sh"]
