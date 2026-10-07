#!/bin/sh
# Patroni owns postgres: it initialises, starts, promotes and demotes it. PGDATA must be a persistent volume.
set -eu
chmod 700 "$PGDATA" 2>/dev/null || true
exec /opt/patroni/bin/patroni /etc/patroni/patroni.yml
