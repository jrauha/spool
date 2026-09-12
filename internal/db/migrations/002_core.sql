CREATE TABLE feeds (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    url text NOT NULL UNIQUE,
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    site_url text NOT NULL DEFAULT '',
    refreshed_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT feeds_url_not_blank CHECK (btrim(url) <> ''),
    CONSTRAINT feeds_title_not_blank CHECK (btrim(title) <> '')
);

CREATE TABLE items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feed_id uuid NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
    guid text NOT NULL DEFAULT '',
    url text NOT NULL DEFAULT '',
    title text NOT NULL,
    summary text NOT NULL DEFAULT '',
    author text NOT NULL DEFAULT '',
    published_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    identity_key text GENERATED ALWAYS AS (COALESCE(NULLIF(guid, ''), NULLIF(url, ''), title)) STORED,
    CONSTRAINT items_title_not_blank CHECK (btrim(title) <> ''),
    CONSTRAINT items_identity_not_blank CHECK (btrim(identity_key) <> '')
);

CREATE UNIQUE INDEX items_feed_identity_idx ON items(feed_id, identity_key);
CREATE INDEX items_feed_id_idx ON items(feed_id);
CREATE INDEX items_published_at_idx ON items(published_at DESC NULLS LAST);
CREATE TABLE events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    entity text NOT NULL,
    entity_id uuid,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT events_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT events_entity_not_blank CHECK (btrim(entity) <> '')
);

CREATE INDEX events_name_idx ON events(name);
CREATE INDEX events_created_at_idx ON events(created_at);
CREATE INDEX events_entity_idx ON events(entity, entity_id);
