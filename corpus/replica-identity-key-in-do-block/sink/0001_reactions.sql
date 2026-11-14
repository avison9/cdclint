-- Invented sink for the entry: a Kafka-engine table reading the captured
-- table. The entry is about the source table's replica identity.
CREATE TABLE kafka_reactions
(
    `userid` String,
    `postid` String,
    `emojiname` String
)
ENGINE = Kafka
SETTINGS
    kafka_broker_list = '${KAFKA_BROKERS}',
    kafka_topic_list = 'mm.public.reactions',
    kafka_group_name = 'clickhouse-reactions',
    kafka_format = 'JSONEachRow';
