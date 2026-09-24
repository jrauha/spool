CREATE TABLE password_resets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    used_at timestamptz,
    CONSTRAINT password_resets_token_hash_not_blank CHECK (btrim(token_hash) <> '')
);

CREATE INDEX password_resets_user_id_idx ON password_resets(user_id);
CREATE INDEX password_resets_expires_at_idx ON password_resets(expires_at);
