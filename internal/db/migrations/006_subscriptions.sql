CREATE TABLE subscriptions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    feed_id uuid NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
    read_before timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, feed_id)
);

CREATE INDEX subscriptions_feed_id_idx ON subscriptions(feed_id);

CREATE TABLE item_read_overrides (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    is_read boolean NOT NULL,
    read_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, item_id),
    CONSTRAINT item_read_overrides_read_at CHECK (NOT is_read OR read_at IS NOT NULL)
);

CREATE INDEX item_read_overrides_item_id_idx ON item_read_overrides(item_id);

INSERT INTO subscriptions (user_id, feed_id)
SELECT users.id, feeds.id
FROM users
CROSS JOIN feeds
ON CONFLICT (user_id, feed_id) DO NOTHING;
