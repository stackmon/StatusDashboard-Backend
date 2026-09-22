# Events

Everything the dashboard publishes is an **event**. `GET /v2/events` and `GET /v2/incidents`
(the deprecated alias) return the same objects.

| `type` | Purpose | Allowed `impact` |
| --- | --- | --- |
| `incident` | Unplanned service degradation | `1` minor, `2` major, `3` outage |
| `maintenance` | Planned maintenance window | `0` only |
| `info` | Informational announcement | `0` only |

The HTTP contract (paths, parameters, schemas, response codes) is defined in
[`openapi.yaml`](../openapi.yaml), served as `/openapi.json` and rendered by the Swagger UI at
`/swagger/index.html`. This document describes the behaviour a schema cannot express: validation,
status lifecycles, automatic transitions, visibility and deprecated aliases.

## Time formats

| API | Layout | Notes |
| --- | --- | --- |
| `/v2/*` | RFC 3339, e.g. `2024-10-02T08:25:00Z` | converted to UTC before storage |

## Components

A component is identified by its `name` plus three attributes: `type`, `region` and `category`.
`POST /v2/components` requires exactly these three attributes, each exactly once, and rejects any
other name ([`checkComponentAttrs`](../internal/api/v2/v2.go)). The `region` attribute is the one
used for filtering, RSS feeds and the availability report.

## Creating an event — `POST /v2/events`

### Request fields

| Field | Required | Rules |
| --- | --- | --- |
| `title` | always | non-empty |
| `type` | always | `incident`, `maintenance` or `info` |
| `impact` | always | `0`–`3`, constrained by `type` (see below) |
| `components` | always | at least one id; every id must exist |
| `start_date` | always | RFC 3339 |
| `end_date` | `maintenance` only | must be after `start_date` |
| `description` | `maintenance` only | non-empty; at most 1500 runes for every type |
| `contact_email` | `maintenance` only | must parse as an e-mail address |
| `system` | no | `true` marks a machine reported system incident |
| `updates` | no | must be empty or omitted at creation |

### Rules

* `impact` must be `0` for `maintenance` and `info`, and non-zero for `incident`.
* An `incident` must not carry an `end_date` and must not start in the future.
* `updates` cannot be pre-seeded — every event starts with the status the server assigns.
* A `system` incident may only be created with `type=incident`; any other combination is rejected.
* A reporter role may only create system incidents; regular events require creator or higher.

### Initial status

| Type | Creator | Operator / Admin |
| --- | --- | --- |
| `incident` | `detected` — "The incident is detected." | same |
| `maintenance` | `pending_review` | `planned` |
| `info` | `planned` | `planned` |

The initial status timestamp is the current time, except when `start_date` lies in the past — then
the status timestamp is backdated to `start_date`.

### Response

```json
{ "result": [ { "component_id": 1, "incident_id": 2, "error": "…" } ] }
```

`200` when every component was processed, `409` when at least one component belongs to an active
maintenance. Component movement is per component and depends on `system`.

#### Regular events (`system` absent or `false`)

1. If no event is open for the component, a new event is created.
2. If the component already belongs to an open **incident**, it is moved to the new event and the
   old incident is closed when the component was its only one.
3. Only `incident` events with an impact greater than zero move components. A component that is
   covered by an open `maintenance` or `info` event is left where it is.

#### System incidents (`system: true`)

For each component the server walks the open events of that component:

1. An open **maintenance** — the component is not touched, its id and an error are returned.
2. An open non-system **incident** — returned as is; the component is not moved.
3. An open system incident with an impact **greater or equal** to the requested one — returned as is.
4. Otherwise the server looks for a system incident with exactly the requested impact:
   * found — the component is moved into it;
   * not found and the current incident has a single component — its impact is raised;
   * not found and the current incident has several components — the component is extracted into a
     new system incident with the requested impact.

The default description of a created system incident is
"System-wide incident affecting multiple components. Created automatically."
See [system-incident-creation.drawio](diagrams/system-incident-creation.drawio) for the decision
graph.

## Statuses

| Type | Open | Closed |
| --- | --- | --- |
| `incident` | `detected`, `analysing`, `fixing`, `impact changed`, `observing`, `resolved` | `reopened`, `changed` |
| `maintenance` | `pending_review`, `reviewed`, `planned`, `in_progress`, `modified`, `completed`, `cancelled` | – |
| `info` | `planned`, `active`, `completed`, `cancelled` | – |

`SYSTEM` is a pseudo status appended to the updates of an incident instead of a normal status
transition; it marks an update that the server superseded (a component was added or moved).

## Automatic transitions

A background checker runs every two minutes and advances stored events:

* `maintenance` — `pending_review` events are ignored; a `cancelled` status short-circuits; a stored
  `reviewed` becomes `planned`; otherwise the status follows the clock: `planned` before
  `start_date`, `in_progress` between `start_date` and `end_date`, `completed` after `end_date`.
* `info` — a `cancelled` status short-circuits, then `planned` / `active` / `completed` follow the
  clock as above.
* Missed transitions are backfilled: `planned` at `start_date`, `in_progress`/`active` at
  `start_date`, `completed` at `end_date`. A cancelled maintenance only receives the backfilled
  `planned` status if it was `reviewed` or already had `planned`.

## Patching an event — `PATCH /v2/events/:eventID`

`message`, `status` and `update_date` are always required, and the status is appended to the updates
list so every patch produces one update. `title`, `description`, `type`, `impact`, `start_date`,
`end_date` and `version` are optional, subject to the rules below. The response is the updated event:
extended fields (`creator`, `contact_email`, `version`) are only returned to authenticated callers —
see [auth.md](auth.md).

### Incident

* A closed incident (`end_date` set) only accepts `reopened` or `changed`, and its dates may only be
  changed together with a `changed` status.
* An open incident follows the open statuses and `changed`; altering `impact` requires the
  `impact changed` status, and the impact must not be lowered to `0`.
* `resolved` sets `end_date` to `update_date`, `reopened` clears it, and `start_date` can only be
  patched while the incident is closed.

### Maintenance

* Operator or admin — unrestricted.
* Creator — only own events, only while the stored status is `pending_review`, and only to
  `pending_review` or `cancelled`.
* `version` is mandatory, enables optimistic locking and is incremented by every successful write
  (a stale `version` yields `409`).

### Info

Info events change status without additional restrictions.

### Update text — `PATCH /v2/events/:eventID/updates/:updateID`

Only the `text` of a single update is replaced. `:updateID` is the **index** of the update in the
event, not a database id, and an out-of-range index returns `404`.

## Extracting components — `POST /v2/events/:eventID/extract`

Moves the listed components out of the given event and returns the resulting events
(`ExtractComponentsToNewIncident`). The `components` array must be non-empty and free of duplicates.
Requires operator or higher and works on `incident` events only.

## Listing events — `GET /v2/events`

Filters: `type` (comma separated), `active`, `status`, `start_date`, `end_date`, `impact`, `system`
and `components` (comma separated ids). An unknown `type`, `status`, `limit` or out-of-range `impact`,
`active=false`, or an `end_date` before `start_date` are rejected with `400`. Pagination uses `page`
(default `1`) and `limit` (one of `10`, `20`, `50`, default `50`) and is reported back as
`pagination`.

`active=true` keeps events without an `end_date` plus events whose window is currently open and
whose status is not a terminal one (`resolved`, `completed`, `cancelled`, `pending_review`,
`reviewed`, `info` completed or cancelled).

```json
{
  "data": [
    {
      "id": 200,
      "title": "OpenStack problem in regions EU-DE/EU-NL",
      "impact": 1,
      "components": [218, 254],
      "start_date": "2025-05-20T10:00:00Z",
      "end_date": null,
      "system": false,
      "type": "incident",
      "status": "fixing",
      "updates": [
        { "id": 0, "status": "detected", "text": "The incident is detected.", "timestamp": "2025-05-20T10:00:00Z" },
        { "id": 1, "status": "fixing", "text": "working on it", "timestamp": "2025-05-20T11:00:00Z" }
      ]
    }
  ],
  "pagination": { "pageIndex": 1, "recordsPerPage": 10, "totalRecords": 1, "totalPages": 1 }
}
```

`updates[].id` is the position of the update in the array, which is the id accepted by
`PATCH …/updates/:updateID`. `GET /v2/incidents` is the deprecated alias; it returns the same
`data` array without pagination.

## Availability — `GET /v2/availability`

Availability is `100 - downtime / totalHours * 100`, rounded to five decimals, and only counts
incidents with an impact of exactly `3` (outage) that have an `end_date`. The window is 12 months
ending with the current one, i.e. it starts on the first day of the month 11 months back; downtime is
clipped to that window.

Every component of `GET /v2/components` is reported as
`{id, name, region, availability: [{year, month, percentage}]}`, with all 12 months listed
(100 % for months without an outage) and the months ordered newest first. Only a component that has
no recorded incident at all comes back with an empty `availability` array.

## RSS — `GET /rss/`

Options are `mt` (region: `EU-DE`, `EU-NL`, `EU-CH2`, `Global`) and `srv` (component name, requires
`mt`). At most 10 incidents are published, using the public visibility rules. Unknown regions or a
`srv` without `mt` return `404`. `/v2/rss/` serves the same feed and exists for tests only.

