-- Webhook notification channels (idempotent: migrations run at every start).
ALTER TABLE notification_channels DROP CONSTRAINT IF EXISTS notification_channels_type_check;
ALTER TABLE notification_channels ADD CONSTRAINT notification_channels_type_check CHECK (type IN ('email','slack','webhook','kafka','amqp'));
