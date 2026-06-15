-- Demo schema for the filtrx examples server. Re-applied on every start, so it
-- drops and recreates the tables, then seeds them.

DROP TABLE IF EXISTS articles;
DROP TABLE IF EXISTS authors;

CREATE TABLE authors (
    id    SERIAL PRIMARY KEY,
    name  TEXT NOT NULL,
    email TEXT NOT NULL UNIQUE
);

CREATE TABLE articles (
    id         SERIAL PRIMARY KEY,
    author_id  INTEGER NOT NULL REFERENCES authors (id),
    title      TEXT NOT NULL,
    body       TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'draft',
    tags       TEXT[] NOT NULL DEFAULT '{}',
    views      INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    -- A generated tsvector keeps full-text search in sync with the content.
    search_vec TSVECTOR GENERATED ALWAYS AS (
        to_tsvector('english', title || ' ' || body)
    ) STORED
);

CREATE INDEX articles_search_idx ON articles USING GIN (search_vec);
CREATE INDEX articles_tags_idx ON articles USING GIN (tags);

INSERT INTO authors (name, email) VALUES
    ('Ada Lovelace', 'ada@example.com'),
    ('Alan Turing', 'alan@example.com'),
    ('Grace Hopper', 'grace@example.com');

INSERT INTO articles (author_id, title, body, status, tags, views, created_at) VALUES
    (1, 'Fast cars and faster Go', 'A deep dive into concurrent code and raw speed in Go.', 'published', '{go,performance}', 420, now() - interval '10 days'),
    (1, 'Notes on analytical engines', 'Draft thoughts on computation before computers.', 'draft', '{history}', 12, now() - interval '9 days'),
    (2, 'Decidability for beginners', 'What can and cannot be computed, explained gently.', 'published', '{theory,history}', 310, now() - interval '8 days'),
    (2, 'Breaking ciphers with Go', 'Using Go concurrency to brute-force toy ciphers fast.', 'published', '{go,security}', 280, now() - interval '7 days'),
    (3, 'The first compiler', 'How a compiler turns human-readable code into machine code.', 'published', '{compilers,history}', 540, now() - interval '6 days'),
    (3, 'Debugging, literally', 'A short, fun history of the very first software bug.', 'archived', '{history}', 90, now() - interval '5 days'),
    (3, 'COBOL is not dead', 'Why a 60-year-old language still runs the world.', 'draft', '{cobol}', 5, now() - interval '4 days');
