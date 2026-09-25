# Spool Architecture

Spool is a hackable feed reader built around a small Go server and trusted external plugins.

This document describes the v1 architecture.

## Core

The Go server owns the basic reader experience:

- HTTP server and web UI
- authentication and sessions
- feed subscriptions
- feed refresh scheduling
- RSS/Atom parsing
- item storage and deduplication
- read/star state
- event persistence
- plugin discovery and execution
- plugin delivery logs
- extension fields
- assets and entity asset attachments

Optional behavior should live in plugins rather than the core.

## Data model

Core tables:

- `users`
- `sessions`
- `feeds`
- `items`
- `events`
- `plugin_deliveries`
- `plugins`
- `entity_fields`
- `assets`
- `entity_assets`

Plugins do not run database migrations in v1. They may store private files under `plugin-data/<plugin-name>/` and may extend core entities through validated field and asset operations.

## Background jobs

River owns job persistence, claims, retries, uniqueness, and periodic-job
leadership. Feature packages define typed job arguments and workers; the
composition root registers them with River. The server inserts request-driven
jobs, worker processes execute refresh jobs, and scheduler processes consume a
dedicated queue that discovers due feeds in bounded batches. River's elected
periodic leader enqueues the shared scan trigger, so worker replicas do not
independently scan feeds. All started River clients register the same periodic
job definitions.

Feed refresh arguments include the feed URL and refresh generation. Workers
ignore stale jobs, and the scheduler excludes feeds with a recorded refresh
error; manual refreshes can still enqueue a new job. Transient errors use
River retries, while known permanent HTTP errors cancel the job. River schema
migrations run through `spool migrate`. The migration drops the
legacy `feed_refresh_jobs` table rather than importing its pending rows; due
feeds are rediscovered by the scheduler after deployment.

## Runtime layout

```text
$SPOOL_HOME/
  site/
    pack/
      local/
        start/
          item-images/
            plugin.json
            bin/item-images
        opt/
          opml/
            plugin.json
            bin/opml
  plugin-data/
    item-images/
      config.json
      cache/
```

- `start/` contains enabled plugins.
- `opt/` contains installed but disabled plugins.
- `plugin-data/<name>/` is private plugin filesystem state.

## Plugin model

Plugins are trusted administrator-installed executables. The server runs them as external binaries or scripts and communicates with them using JSON over stdin/stdout.

Each plugin is an installed directory with a `plugin.json` manifest:

```json
{
  "name": "item-images",
  "version": "0.1.0",
  "apiVersion": "1",
  "entry": "bin/item-images",
  "description": "Extracts hero images from item pages",
  "hooks": ["item.created"],
  "fields": [
    {
      "entity": "item",
      "name": "hero_image",
      "type": "url",
      "label": "Hero image"
    }
  ]
}
```

Manifest rules:

- `name` is stable and unique.
- `entry` is relative to the plugin directory.
- `hooks` lists events the plugin receives.
- `fields` declares extension fields the plugin may write.

## Events

Core emits persisted events for feed and item activity, including:

- `feed.added`
- `feed.updated`
- `feed.error`
- `item.created`
- `item.read`
- `item.unread`
- `item.starred`
- `item.unstarred`
- `refresh.started`
- `refresh.finished`

Delivery flow:

1. Persist the event.
2. Find enabled plugins subscribed to the event hook.
3. Create a `plugin_deliveries` row.
4. Execute the plugin with a timeout.
5. Send invocation JSON on stdin.
6. Capture stdout and stderr with size limits.
7. Decode response operations from stdout.
8. Validate operations.
9. Apply valid operations in a transaction.
10. Mark delivery succeeded or failed.

Plugin failures are recorded but do not fail the core feed refresh transaction.

## Plugin protocol

Core invokes the plugin entry:

```bash
$SPOOL_HOME/site/pack/local/start/item-images/bin/item-images
```

Invocation JSON is sent on stdin:

```json
{
  "apiVersion": "1",
  "deliveryId": "delivery-id",
  "plugin": {
    "name": "item-images",
    "version": "0.1.0",
    "dataDir": "/var/lib/spool/plugin-data/item-images"
  },
  "event": {
    "name": "item.created",
    "timestamp": "2026-09-12T12:00:00Z",
    "feed": {
      "id": "feed-id",
      "url": "https://example.com/feed.xml",
      "title": "Example"
    },
    "item": {
      "id": "item-id",
      "feedId": "feed-id",
      "url": "https://example.com/post",
      "title": "Example post",
      "summary": "..."
    }
  }
}
```

Plugin logs go to stderr. Plugin responses go to stdout:

```json
{
  "ops": [
    {
      "op": "asset.attach",
      "entity": "item",
      "entityId": "item-id",
      "role": "hero_image",
      "kind": "image",
      "url": "https://example.com/image.jpg",
      "replace": true
    },
    {
      "op": "field.set",
      "entity": "item",
      "entityId": "item-id",
      "name": "hero_image",
      "value": "https://example.com/image.jpg"
    }
  ]
}
```

Supported v1 operations:

- `field.set`
- `field.delete`
- `asset.attach`
- `asset.detach`
- `item.mark_read`
- `item.mark_unread`
- `item.star`
- `item.unstar`

Core validates every operation before applying it.

## Go plugin SDK

The Go SDK wraps the raw JSON protocol:

```go
package main

import (
    "strings"

    "github.com/spool-reader/plugin-sdk-go/spool"
)

func main() {
    spool.Run(spool.Plugin{
        Handlers: spool.Handlers{
            ItemCreated: func(ctx spool.Context, event spool.ItemCreatedEvent) error {
                title := strings.ToLower(event.Item.Title)
                if strings.Contains(title, "postgres") {
                    ctx.Ops.StarItem(event.Item.ID)
                }
                return nil
            },
        },
    })
}
```

The SDK handles stdin/stdout encoding, API version checks, typed events, typed operation builders, stderr logging, plugin data-dir helpers, and fixture testing helpers.

Plugins may also implement the raw JSON protocol directly in any language.

## First-party plugins

First-party plugins use the same external plugin format as third-party plugins.

Initial plugins:

- `item-images` — fetch item pages, extract Open Graph/Twitter image, attach `hero_image` asset.
- `feed-icons` — discover feed/site favicons and attach feed icon asset.
- `opml` — import/export subscriptions.
- `webhooks` — deliver selected events to configured URLs.
- `rules` — user-defined item automation.
- `tags` — item tagging using extension fields.

## Packaging

Core v1 delivery:

- single `spool` binary
- Docker image
- Docker Compose file with Postgres

Plugin v1 delivery:

- copied or checked-out plugin directory under `site/pack/local/opt`
- compiled plugin binary under `bin/`
- enabled by moving or symlinking the plugin directory into `site/pack/local/start`

## Operational guardrails

Plugins run with the same privileges as the Spool server user. Installing a plugin is equivalent to allowing that program to run as that user.

The core still applies reliability boundaries:

- plugin execution timeouts
- stdout/stderr size limits
- no database credentials passed to plugins by default
- validation of all returned operations
- transactional operation application
- persisted delivery logs and failures
