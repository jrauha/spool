# Spool

[![CI](https://github.com/jrauha/spool/actions/workflows/ci.yml/badge.svg)](https://github.com/jrauha/spool/actions/workflows/ci.yml)
[![Coverage](https://codecov.io/gh/jrauha/spool/branch/main/graph/badge.svg)](https://codecov.io/gh/jrauha/spool)

Spool is a self-hostable, plugin-first feed reader.

## Development

Requirements:

- Go 1.22+

Common commands:

```sh
make test
make run
make watch
make build
```

`make watch` runs Air for the server, River worker, and scheduler; each role is
rebuilt and restarted when Go files change. The default command starts only the
server, which listens on `:8080` by default.
Override the address with `SPOOL_ADDR`. Run feed processing with `spool worker`
and periodic discovery with `spool scheduler`. River coordinates periodic jobs
across active worker and scheduler clients.

Run `spool migrate` to apply Spool and River schema migrations.

Spool uses Postgres. Set `SPOOL_DATABASE_URL` before running, for example:

```sh
export SPOOL_DATABASE_URL='postgres://spool:spool@127.0.0.1:5432/spool?sslmode=disable'
```

Spool also loads a `.env` file from the current directory if one exists:

```env
SPOOL_DATABASE_URL=postgres://spool:spool@127.0.0.1:5432/spool?sslmode=disable
```

Set `SPOOL_TEST_DATABASE_URL` to run Postgres integration tests.

Password reset email is enabled when `SPOOL_SMTP_ADDR` is set. Configure
`SPOOL_PUBLIC_URL` (an HTTPS URL), `SPOOL_SMTP_FROM`, and optionally
`SPOOL_SMTP_USERNAME` and `SPOOL_SMTP_PASSWORD`. The SMTP server must support
STARTTLS.

## Item queries

The search page supports plain-text and RSQL modes. Text mode searches item
titles, authors, summaries, and URLs. RSQL mode supports filters such as:

```text
text=search='postgres replication';read==false
feed.id=in=(2f079ce2-b5ad-4c8d-9e90-93e735ba1378,58b82b8b-b740-43d5-9711-aaa582217b6a)
date>=2025-01-01T00:00:00Z;(author==Alice,title==*Postgres*)
```

Use `;` for AND, `,` for OR, and parentheses for grouping. Supported selectors
are `id`, `feed.id`, `feed.title`, `title`, `author`, `url`, `publishedAt`,
`createdAt`, `date`, `read`, and `text`. Comparison operators are `==`, `!=`,
`>`, `>=`, `<`, `<=`, `=in=`, and `=out=`. The `text` selector uses the
`=search=` operator and PostgreSQL web-search syntax.
