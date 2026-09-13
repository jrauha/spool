CREATE TABLE feed_refresh_jobs (
    feed_id uuid PRIMARY KEY REFERENCES feeds(id) ON DELETE CASCADE,
    available_at timestamptz NOT NULL,
    lease_token uuid,
    lease_until timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT feed_refresh_jobs_attempts_nonnegative CHECK (attempts >= 0)
);

CREATE INDEX feed_refresh_jobs_available_at_idx
    ON feed_refresh_jobs(available_at);
