WITH inserted AS (
    INSERT INTO feeds (url, title, description, site_url)
    VALUES
        (
            'https://go.dev/blog/feed.atom',
            'Go Blog',
            'Official news and articles from the Go team.',
            'https://go.dev/blog/'
        ),
        (
            'https://www.postgresql.org/rss/news.xml',
            'PostgreSQL News',
            'News from the PostgreSQL project.',
            'https://www.postgresql.org/'
        ),
        (
            'https://news.ycombinator.com/rss',
            'Hacker News',
            'Technology and startup discussions.',
            'https://news.ycombinator.com/'
        ),
        (
            'https://lobste.rs/rss',
            'Lobsters',
            'Computing-focused community discussions.',
            'https://lobste.rs/'
        )
    ON CONFLICT (url) DO NOTHING
    RETURNING id, url
)
INSERT INTO events (name, entity, entity_id, payload)
SELECT 'feed.added', 'feed', id, jsonb_build_object('url', url)
FROM inserted;
