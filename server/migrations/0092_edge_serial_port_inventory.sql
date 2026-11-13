CREATE TABLE IF NOT EXISTS edge_serial_port_inventory (
 gateway_id text PRIMARY KEY REFERENCES gateways(id),
 tenant_id text NOT NULL REFERENCES tenants(id),
 ports jsonb NOT NULL,
 reported_at timestamptz NOT NULL DEFAULT now()
);
