ALTER TABLE items ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
    setweight(to_tsvector('simple', coalesce(author, '')), 'B') ||
    setweight(to_tsvector('simple', left(coalesce(summary, ''), 100000)), 'C') ||
    setweight(to_tsvector('simple', coalesce(url, '')), 'D')
) STORED;

CREATE INDEX items_search_vector_idx ON items USING GIN (search_vector);
CREATE INDEX items_feed_sort_at_idx ON items (feed_id, (coalesce(published_at, created_at)) DESC, id DESC);
CREATE INDEX items_sort_at_idx ON items ((coalesce(published_at, created_at)) DESC, id DESC);
