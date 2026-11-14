-- Invented sink for the entry: a Kafka-engine table reading the captured
-- table. The entry is about the source table's replica identity.
CREATE TABLE kafka_sessions
(
    `id` String,
    `user_id` String,
    `expires_at` String
)
ENGINE = Kafka
SETTINGS
    kafka_broker_list = '${KAFKA_BROKERS}',
    kafka_topic_list = 'app.public.sessions',
    kafka_group_name = 'clickhouse-sessions',
    kafka_format = 'JSONEachRow';
