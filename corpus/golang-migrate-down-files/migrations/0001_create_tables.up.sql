-- Reduced from Mattermost v10.11.0 000002 and 000016, keys as Mattermost
-- declares them: teammembers' in its CREATE TABLE, reactions' in a DO block
-- (cdclint does not run DO bodies, so it does not judge reactions' key).
CREATE TABLE reactions (
    userid    VARCHAR(26) NOT NULL,
    postid    VARCHAR(26) NOT NULL,
    emojiname VARCHAR(64) NOT NULL,
    createat  BIGINT
);

DO $$
BEGIN
    ALTER TABLE reactions ADD PRIMARY KEY (postid, userid, emojiname);
END $$;

CREATE TABLE teammembers (
    teamid VARCHAR(26) NOT NULL,
    userid VARCHAR(26) NOT NULL,
    PRIMARY KEY (teamid, userid)
);
