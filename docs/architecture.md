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
- cached assets and item asset attachments

The reader's thumbnail pipeline lives in core; optional image roles can be
provided by plugins.

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
- `item_assets`

`assets` records cached bytes (ID, media type, size, checksum, creation time);
files live under `$SPOOL_HOME/assets/` with opaque names derived from asset IDs.
`item_assets` attaches an asset to an item by role, with one asset per item and
role. The thumbnail uses the `thumbnail` role. Deleting an item removes its
attachments; unreferenced asset records and files are cleaned up separately.
There is no feed asset attachment or discovery checkpoint table. `items.url`
and `items.image_url` hold the page URL and preferred feed-provided image URL
used as crawl inputs, not local cache URLs.

Plugins do not run database migrations in v1. They may store private files under
`plugin-data/<plugin-name>/` and may extend core entities through validated
field and item asset operations.

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
migrations run through `spool migrate`.

New items and changes to an item's page URL or feed-provided image URL enqueue
unique thumbnail jobs on a low-concurrency queue when at least one input is
present. Clearing both inputs removes the thumbnail attachment in the item
upsert transaction without a crawl. The item upsert and River insert happen in
one database transaction, so a failed enqueue cannot lose a crawl. Unchanged
items do not enqueue jobs; River retries failures and deduplicates jobs only
while they are active. Workers prefer the feed image URL, otherwise inspect
page metadata, then fetch and validate raster images. Before storage,
still-image sources are center-cropped to 16:10 and downscaled to at most
480×300. Opaque images are encoded as JPEG at quality 82, while transparency
is preserved as PNG.
Animated GIFs remain unchanged. Workers write cache files atomically and
replace the item's `thumbnail` attachment only when the job's image inputs are
still current. A definitive lack of an image removes that attachment. The
server serves assets through authenticated local URLs,
checking access via the attached item and the user's subscription. Periodic
cleanup removes unreferenced asset records and files after a grace period.

## Runtime layout

```text
$SPOOL_HOME/
  assets/
    <asset-id>
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
- `assets/` stores cached bytes shared by the server and workers.

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
      "imageUrl": "https://example.com/image.jpg",
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
      "itemId": "item-id",
      "role": "thumbnail",
      "url": "https://example.com/image.jpg"
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

- `item-images` — fetch item pages, extract Open Graph/Twitter image, attach a `thumbnail` asset.
- `feed-icons` — discover feed/site favicon URLs for feed metadata; caching
  feed icons would require a separate feed attachment model.
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
