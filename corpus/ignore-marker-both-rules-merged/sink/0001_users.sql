CREATE TABLE kafka_users
(
    `id` String,
    `email` String
)
ENGINE = Kafka
SETTINGS
    kafka_broker_list = '${KAFKA_BROKERS}',
    kafka_topic_list = 'app.public.users',
    kafka_group_name = 'clickhouse-users',
    kafka_format = 'JSONEachRow';
