-- Invented sink for the entry: a Kafka-engine table reading the captured
-- table. The entry is about the source table's replica identity.
CREATE TABLE kafka_audit_events
(
    `actor_id` String,
    `action` String,
    `created_at` String
)
ENGINE = Kafka
SETTINGS
    kafka_broker_list = '${KAFKA_BROKERS}',
    kafka_topic_list = 'app.public.audit_events',
    kafka_group_name = 'clickhouse-audit_events',
    kafka_format = 'JSONEachRow';
