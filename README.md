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
make build
```

The server listens on `:8080` by default. Override with `SPOOL_ADDR`.

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
