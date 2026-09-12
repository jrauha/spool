# Spool

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
export SPOOL_DATABASE_URL='postgres://spool:spool@localhost:5432/spool?sslmode=disable'
```

For local development, Spool also loads a `.env` file from the current directory if one exists:

```env
SPOOL_DATABASE_URL=postgres://spool:spool@localhost:5432/spool?sslmode=disable
```

Set `SPOOL_TEST_DATABASE_URL` to run Postgres integration tests.
