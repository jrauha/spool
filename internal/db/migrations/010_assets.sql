ALTER TABLE items
    ADD COLUMN image_url text NOT NULL DEFAULT '';

ALTER TABLE items
    ADD CONSTRAINT items_image_url_length
    CHECK (char_length(image_url) <= 4096) NOT VALID;

CREATE TABLE assets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    media_type text NOT NULL,
    byte_size bigint NOT NULL,
    sha256 text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT assets_media_type_not_blank CHECK (btrim(media_type) <> ''),
    CONSTRAINT assets_byte_size_nonnegative CHECK (byte_size >= 0),
    CONSTRAINT assets_sha256_format CHECK (sha256 ~ '^[a-f0-9]{64}$')
);

CREATE TABLE item_assets (
    item_id uuid NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    role text NOT NULL,
    asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (item_id, role),
    CONSTRAINT item_assets_role_not_blank CHECK (btrim(role) <> '')
);

CREATE INDEX item_assets_asset_id_idx ON item_assets (asset_id);
