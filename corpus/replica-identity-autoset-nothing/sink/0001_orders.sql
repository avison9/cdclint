-- Invented sink for the entry: a Kafka-engine table reading the captured
-- table. The entry is about the source table's replica identity.
CREATE TABLE kafka_orders
(
    `id` String,
    `status` String
)
ENGINE = Kafka
SETTINGS
    kafka_broker_list = '${KAFKA_BROKERS}',
    kafka_topic_list = 'app.public.orders',
    kafka_group_name = 'clickhouse-orders',
    kafka_format = 'JSONEachRow';
