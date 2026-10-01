# Maintenance Email Notifications

Maintenance events can trigger email notifications delivered through a **transactional outbox**.
The feature is off by default; enabling it requires SMTP transport settings and at least one
review recipient.

## How it works

1. **Enqueue (in-transaction).** When a maintenance event is created or its status changes, the
   notification rows are written to the `notification_outbox` table **inside the same database
   transaction** as the business change. If the transaction rolls back, no notification is
   published. A unique `dedup_key` (`incidentID:kind:oldStatus>newStatus:recipient`) prevents
   duplicate rows when the same transition is published twice (e.g. a caller retry or a race
   between the checker and an API edit).

2. **Wake.** After the transaction commits, the publisher signals the delivery worker
   (`Publisher.Notify()`), which is a non-blocking channel send.

3. **Deliver (out-of-transaction).** A background worker claims one row at a time with a lease
   (`locked_by` / `locked_at`), renders the email from the stored payload, and sends it over
   SMTP. Sending happens **outside** any database transaction.

4. **Retry.** On a transient SMTP failure the row stays `pending` with an incremented
   `attempts` counter and a `next_attempt_at` set to now + exponential backoff
   (`base * 2^(n-1)`, capped at 2 h, jittered ±20 %). After `SD_NOTIFICATIONS_MAX_ATTEMPTS`
   failures the row is marked `failed` (terminal).

5. **Stale recovery.** A periodic sweep (every 5 min) re-claims `processing` rows whose lease
   has expired (e.g. a pod crashed mid-send) and retries them.

6. **Retention.** `sent` rows are pruned after 30 days. `failed` rows are kept indefinitely so
   they can be inspected and re-driven.

## When a notification is sent

A notification is enqueued **only on a real status transition** of a `maintenance` event.
No-op patches (same status) do not re-notify.

| New status | Notification kind | Recipients |
| --- | --- | --- |
| `pending_review` | `pending_review` | Review audience (SMOD + operators + admins) **plus** the creator |
| `reviewed` | `reviewed` | Review audience **plus** the creator |
| `planned` | `status_changed` | Creator only |
| `in_progress` | `status_changed` | Creator only |
| `completed` | `status_changed` | Creator only |
| `cancelled` | `status_changed` | Creator only |

The review audience is the union of `SD_NOTIFICATIONS_SMOD_EMAIL`,
`SD_NOTIFICATIONS_EMAILS_OPERATORS` and `SD_NOTIFICATIONS_EMAILS_ADMINS`. These addresses come
from trusted configuration and are **not** subject to the domain allow-list.

The creator address (`contact_email` on the event) is subject to:

* **Domain allow-list** (`SD_NOTIFICATIONS_ALLOWED_DOMAINS`) — when set, only addresses whose
  domain is in the list receive notifications. An empty list accepts all domains.
* **Exclusion list** (`SD_NOTIFICATIONS_EXCLUDED_EMAILS`) — addresses in this list are dropped
  from every recipient list, including the review audience. This cannot be bypassed by passing
  the address as `contact_email`.

All addresses are normalised (trimmed, lowercased) and deduplicated before enqueuing.

## Triggers

| Source | When |
| --- | --- |
| `POST /v2/events` (create) | A new maintenance event is created. The initial status is `pending_review` (creator) or `planned` (operator/admin). |
| `PATCH /v2/events/:id` | The stored status actually changes. A patch that keeps the same status does not notify. |
| Background checker | The checker advances a maintenance status based on the clock (e.g. `reviewed` → `planned`, `planned` → `in_progress`, `in_progress` → `completed`). |

## Email format

Subject:

```
[Maintenance] {title} — {state}
```

Body:

```
Maintenance "{title}" (event #{id}) {headline}.

Status: {old} -> {new}
Changed by: {actor}
Time (UTC): {timestamp}

Details: {link}
```

`{link}` is `{SD_WEB_URL}/incidents/{id}`.

`{state}` and `{headline}` are derived from the old and new status:

| Situation | `{state}` | `{headline}` |
| --- | --- | --- |
| Status change (old status set) | the new status | `changed status` |
| Created as `pending_review` | `awaiting review` | `has been submitted for review` |
| Created as any other status | `scheduled` | `has been scheduled` |

For a creation (no old status) the body omits the `->` and reads `Status: {new}` with
`Created by: {actor}` instead of `Changed by`.

## Operations

All endpoints require the **admin** role.

| Endpoint | Description |
| --- | --- |
| `GET /v2/notifications/stats` | Queue statistics: pending, processing, failed, stale-processing, retry-backlog counts and oldest pending age. |
| `GET /v2/notifications/failed` | List outbox rows by status (`?status=pending\|processing\|sent\|failed`, default `failed`) with `?limit=` (default 100, max 1000). |
| `POST /v2/notifications/redrive` | Reset `failed` rows back to `pending` and wake the worker. Optional JSON body `{"ids": [1, 2]}` limits the re-drive; an empty body re-drives all failed rows. |

### Prometheus metrics

Served on the dedicated metrics port (`SD_METRICS_PORT`, default `9090`), **not** on the public
API port. The metrics listener is only started when notifications are enabled.

| Metric | Type | Description |
| --- | --- | --- |
| `notification_sent_total{kind}` | Counter | Emails accepted by the mail server, by kind. |
| `notification_failed_total{kind}` | Counter | Send failures (retryable and terminal), by kind. |
| `notification_attempts_total` | Counter | Total delivery attempts. |
| `notification_stale_recovered_total` | Counter | Processing rows recovered after a lease timeout. |
| `notification_delivery_duration_seconds` | Histogram | Time to render and send one notification. |
| `notification_outbox_pending` | Gauge | Rows waiting to be sent. |
| `notification_outbox_processing` | Gauge | Rows currently being sent. |
| `notification_outbox_failed` | Gauge | Rows in the terminal failed state. |
| `notification_outbox_stale_processing` | Gauge | Processing rows whose lease has expired. |
| `notification_outbox_retry_backlog` | Gauge | Pending rows waiting for a future retry. |
| `notification_outbox_oldest_pending_age_seconds` | Gauge | Age of the oldest undelivered row. |
| `notification_collector_errors_total` | Counter | Failures to read outbox queue depth at scrape time. |

## Configuration

All variables are prefixed with `SD_`. When `SD_NOTIFICATIONS_ENABLED` is `false` (the default),
none of the other notification variables are required and the feature is inert.

### Master switch

| Variable | Default | Notes |
| --- | --- | --- |
| `SD_NOTIFICATIONS_ENABLED` | `false` | Master on/off switch. |

### SMTP transport (required when enabled)

| Variable | Default | Notes |
| --- | --- | --- |
| `SD_SMTP_HOST` | – | **Required** when enabled. SMTP server hostname. |
| `SD_SMTP_PORT` | – | **Required** when enabled. TCP port (1–65535). |
| `SD_SMTP_FROM` | – | **Required** when enabled. Sender address, must parse as a valid e-mail. |
| `SD_SMTP_USER` | – | Optional. SMTP AUTH username. When set, the auth mechanism is auto-discovered. |
| `SD_SMTP_PASSWORD` | – | Optional. SMTP AUTH password. |
| `SD_SMTP_TLS` | `false` | `true` enforces mandatory TLS; `false` uses opportunistic TLS (STARTTLS when offered). |
| `SD_SMTP_TIMEOUT` | `30s` | Go duration string for the SMTP connect/send timeout. |

### Delivery tuning

| Variable | Default | Notes |
| --- | --- | --- |
| `SD_NOTIFICATIONS_LEASE_TIMEOUT` | `60s` | Go duration. Must be **greater than** `SD_SMTP_TIMEOUT`. A processing row whose lease expires is re-claimed by the sweep. |
| `SD_NOTIFICATIONS_MAX_ATTEMPTS` | `5` | Finite retry limit before a row is marked `failed`. |
| `SD_NOTIFICATIONS_BACKOFF_INTERVAL` | `5m` | Go duration. Base delay for exponential retry backoff (`base * 2^(n-1)`, capped at 2 h, jittered ±20 %). |

### Recipients

| Variable | Default | Notes |
| --- | --- | --- |
| `SD_NOTIFICATIONS_SMOD_EMAIL` | – | Fixed SMOD team review recipient. |
| `SD_NOTIFICATIONS_EMAILS_OPERATORS` | – | Comma-separated review recipients for the Operator role. |
| `SD_NOTIFICATIONS_EMAILS_ADMINS` | – | Comma-separated review recipients for the Admin role. |
| `SD_NOTIFICATIONS_ALLOWED_DOMAINS` | – | Comma-separated domain allow-list for the user-supplied `contact_email`. Empty means any domain is accepted. |
| `SD_NOTIFICATIONS_EXCLUDED_EMAILS` | – | Comma-separated addresses that never receive notifications. Applied to every recipient, including the review audience. |

At least one of `SD_NOTIFICATIONS_SMOD_EMAIL`, `SD_NOTIFICATIONS_EMAILS_OPERATORS` or
`SD_NOTIFICATIONS_EMAILS_ADMINS` must be set when notifications are enabled.

### Deep links

| Variable | Default | Notes |
| --- | --- | --- |
| `SD_WEB_URL` | – | Web origin used to build the maintenance deep link in the email body, e.g. `https://status.example.com`. |

### Metrics

| Variable | Default | Notes |
| --- | --- | --- |
| `SD_METRICS_PORT` | `9090` | Port for the dedicated `/metrics` listener. Must differ from `SD_PORT`. The listener is only started when notifications are enabled. |

## Example `.env`

```dotenv
# Enable maintenance email notifications
SD_NOTIFICATIONS_ENABLED=true

# SMTP transport
SD_SMTP_HOST=smtp.otc.example.com
SD_SMTP_PORT=587
SD_SMTP_FROM=status-dashboard@otc.example.com
SD_SMTP_USER=sd-mailer
SD_SMTP_PASSWORD=changeme
SD_SMTP_TLS=false
SD_SMTP_TIMEOUT=30s

# Delivery tuning
SD_NOTIFICATIONS_LEASE_TIMEOUT=60s
SD_NOTIFICATIONS_MAX_ATTEMPTS=5
SD_NOTIFICATIONS_BACKOFF_INTERVAL=5m

# Review audience (at least one required)
SD_NOTIFICATIONS_SMOD_EMAIL=smod@otc.example.com
SD_NOTIFICATIONS_EMAILS_OPERATORS=ops1@otc.example.com,ops2@otc.example.com
SD_NOTIFICATIONS_EMAILS_ADMINS=admin@otc.example.com

# Optional: restrict creator contact_email to these domains
# SD_NOTIFICATIONS_ALLOWED_DOMAINS=otc.example.com,partner.example.com

# Optional: addresses that never receive notifications
# SD_NOTIFICATIONS_EXCLUDED_EMAILS=bot@otc.example.com

# Deep link origin
SD_WEB_URL=https://status.otc.example.com

# Metrics port (default 9090)
# SD_METRICS_PORT=9090
```

## Database

The `notification_outbox` table is created by migration `000008_notification.up.sql`. It is
applied automatically by `make migrate-up`. No manual setup is required.
