-- Invented sink for the entry: two Kafka-engine tables reading a few of the
-- captured columns. The entry is about the source reader, not the sink.
CREATE TABLE kafka_emoji
(
    `id` String,
    `name` String,
    `deleteat` Int64
)
ENGINE = Kafka
SETTINGS
    kafka_broker_list = '${KAFKA_BROKERS}',
    kafka_topic_list = 'mm.public.emoji',
    kafka_group_name = 'clickhouse-emoji',
    kafka_format = 'JSONEachRow';

CREATE TABLE kafka_sharedchannelremotes
(
    `id` String,
    `channelid` String,
    `remoteid` String,
    `lastpostupdateat` Int64
)
ENGINE = Kafka
SETTINGS
    kafka_broker_list = '${KAFKA_BROKERS}',
    kafka_topic_list = 'mm.public.sharedchannelremotes',
    kafka_group_name = 'clickhouse-sharedchannelremotes',
    kafka_format = 'JSONEachRow';
