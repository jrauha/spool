ALTER TABLE items
    ADD CONSTRAINT items_title_length CHECK (char_length(title) <= 1000) NOT VALID,
    ADD CONSTRAINT items_author_length CHECK (char_length(author) <= 500) NOT VALID,
    ADD CONSTRAINT items_summary_length CHECK (char_length(summary) <= 100000) NOT VALID,
    ADD CONSTRAINT items_url_length CHECK (char_length(url) <= 4096) NOT VALID;

-- Existing oversized values remain valid until their feeds refresh. Bound the
-- indexed projection so those rows cannot exceed PostgreSQL's tsvector limit.
ALTER TABLE items ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', left(coalesce(title, ''), 1000)), 'A') ||
    setweight(to_tsvector('simple', left(coalesce(author, ''), 500)), 'B') ||
    setweight(to_tsvector('simple', left(coalesce(summary, ''), 100000)), 'C') ||
    setweight(to_tsvector('simple', left(coalesce(url, ''), 4096)), 'D')
) STORED;

CREATE INDEX items_search_vector_idx ON items USING GIN (search_vector);
CREATE INDEX items_feed_sort_at_idx ON items (feed_id, (coalesce(published_at, created_at)) DESC, id DESC);
CREATE INDEX items_sort_at_idx ON items ((coalesce(published_at, created_at)) DESC, id DESC);
