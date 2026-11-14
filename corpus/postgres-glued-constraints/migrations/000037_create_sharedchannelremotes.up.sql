-- Mattermost v10.11.0, 000037_create_sharedchannelremotes.up.sql, as
-- written: both table constraints are glued to their parenthesis.
CREATE TABLE IF NOT EXISTS sharedchannelremotes (
    id character varying(26) NOT NULL,
    channelid character varying(26) NOT NULL,
    creatorid character varying(26),
    createat bigint,
    updateat bigint,
    isinviteaccepted boolean,
    isinviteconfirmed boolean,
    remoteid character varying(26),
    PRIMARY KEY(id, channelid),
    UNIQUE(channelid, remoteid)
);

ALTER TABLE sharedchannelremotes ADD COLUMN IF NOT EXISTS LastPostUpdateAt bigint;
ALTER TABLE sharedchannelremotes ADD COLUMN IF NOT EXISTS LastPostId character varying(26);
