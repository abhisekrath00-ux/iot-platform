-- Webhook/Kafka/AMQP notification channels. The type constraint is (re)defined in 0060, the last migration
-- that touches it. This file used to drop and re-add a narrower list on every start, which fails once a
-- channel of a later type exists, so it is now a no-op (migrations run at every start and must be idempotent).
SELECT 1;
