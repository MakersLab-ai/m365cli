# Watching mail (`m365 mail watch poll`)

`m365 mail watch poll` is a long-running loop that delta-polls one or more
mailboxes and forwards new mail to a webhook. App-only, no public inbound
endpoint required.

## Usage

```bash
m365 mail watch poll \
  --mailbox agent@contoso.com \      # repeatable; or --all (see "Which mailboxes")
  --folder inbox \                   # default: inbox
  --interval 30s \                   # default: 30s
  --hook-url https://example/hook \  # required
  --hook-token <bearer> \            # optional
  --include-body --max-bytes 20000   # body is opt-in
```

Tuning flags (Graph engine, defaults shown):

| Flag | Default | Meaning |
| --- | --- | --- |
| `--catchup-max` | `10` | After a re-prime, deliver at most this many recent messages. `-1` disables the catch-up (`0` means the default). |
| `--catchup-window` | `24h` | The catch-up never reaches further back than this. |
| `--catchup-max-age` | `48h` | If the last good state is older than this, there is no catch-up at all: the mailbox resumes "from now". |
| `--pending-max-age` | `24h` | An undelivered message is dropped (logged) after waiting this long. |
| `--pending-max-attempts` | `100` | ... or after this many failed delivery attempts. |
| `--max-pages-per-poll` | `500` | Delta pages per mailbox per poll (200 ids each). A larger prime continues on the next poll. |
| `--poll-deadline` | `3m` | Delta paging time per mailbox per poll, so one big mailbox cannot stall the others. |

`--include-changed` / `--include-deleted` still work but switch to the v1
engine (see "Engines").

### Which mailboxes (`--all` and `watch_mailboxes`)

Each mailbox is checked against `allowed_mailboxes` before any request.
`--all` watches:

- the `watch_mailboxes` list from `config.toml`, when it is set, or
- every concrete (non-glob) `allowed_mailboxes` entry otherwise (the default,
  unchanged from earlier versions).

```toml
allowed_mailboxes = ["office@contoso.com", "invoices@contoso.com"]
watch_mailboxes   = ["office@contoso.com"]   # read both, watch only one
```

Every `watch_mailboxes` entry must be a concrete address that
`allowed_mailboxes` permits; otherwise the watcher refuses to start. (Other
commands are not affected by a `watch_mailboxes` typo.)

### Off switch

`~/.config/m365cli/watch.disabled` is honoured by the supervisor that starts
the watcher, not by `m365` itself; nothing here changes that contract.

## Payload

POSTed as JSON (gog-compatible, unchanged):

```json
{
  "source": "m365",
  "account": "agent@contoso.com",
  "messages": [
    { "id": "...", "threadId": "...", "from": "...", "to": "...",
      "subject": "...", "date": "...", "snippet": "...",
      "body": "...", "bodyTruncated": false, "labels": ["INBOX", "UNREAD"] }
  ],
  "deletedMessageIds": ["..."]
}
```

`body` is present only with `--include-body`; `deletedMessageIds` only with
`--include-deleted` (v1 engine). One POST per poll carries up to 50 messages.

## Engines

| Backend / flags | Engine | State file |
| --- | --- | --- |
| Graph (default) | **v2** (this page) | `watch/<mailbox>__<folder>.v2.json` |
| EWS, or `--include-changed` / `--include-deleted` | v1 (unchanged) | `watch/<mailbox>__<folder>.json` |

The startup line names the engine: `polling [...] ... [state v2]`.

## How the v2 engine works (Graph)

Measured on a real 35k-message mailbox: Graph keeps only a few message-delta
sync-state generations and creates **one generation per page**, so a
`nextLink`/`deltaLink` held two pages back answers `410 Gone`
(`SyncStateNotFound`). v0.2.x stored its cursor only after the hook accepted
the whole round; a failing hook (or a 5xx mid-pagination) replayed the old
link until it was evicted, and the mailbox stayed dead. v2 is built around
that model:

1. **IDs-only delta.** The delta request is
   `mailFolders/<folder>/messages/delta?changeType=created&$select=id` with
   `Prefer: odata.maxpagesize=200`. `changeType` and `$select` go on the
   initial request only; Graph carries them inside the returned links.
   `@removed` entries are ignored. An id is ~270 bytes on the wire instead of
   ~35 KB with bodies.
2. **Cursor persisted per page.** After every page the state file is written
   atomically (temp file, fsync, rename, fsync of the directory) with the new
   `next_link` (or the final `delta_link`) and the newly found ids in
   `pending`. A crash or a 5xx mid-pagination resumes from the last saved
   page, never from the round start.
3. **Per-message fetch.** Each pending id is fetched with
   `GET messages/{id}?$select=<production fields>` (body only with
   `--include-body`) and the payload is built exactly as before. A `404`
   (moved or deleted meanwhile) is skipped and logged, never retried.
4. **Delivery never holds the cursor.** If the hook fails, the cursor has
   already advanced; the ids stay in `pending` and are retried with backoff
   (30 s doubling to 5 min) until delivered, or dropped with a log line after
   `--pending-max-age` / `--pending-max-attempts`. At most 500 ids are kept
   pending (oldest dropped first, logged). This is a bridge until a real
   outbox (one message per POST) lands.
5. **Second tick = 0 POSTs.** After a delivery, a restart or a re-prime, a
   poll with no new mail sends nothing.
6. **Floor.** Old mail moved or restored into the folder shows up as
   "created". Mail received before the floor (prime start minus 10 min) is not
   delivered as new; catch-up mail is exempt.

### First run, token loss and re-prime

- **First run** (no state at all): IDs-only prime. Every existing id is marked
  seen; nothing is delivered. Memory stays flat: ids are streamed page by page
  and the seen list is capped (a 35k mailbox primed in 55 s at 14 MB RSS in
  the measurement).
- **Token loss** (`syncStateNotFound` on any 4xx, matched case-insensitively —
  observed as `410`, documented also as `400`): automatic IDs-only re-prime in
  the same poll. The prime itself delivers nothing.
- **Catch-up after a re-prime.** Once the prime has its deltaLink, one list
  query fetches mail received since the **anchor**:
  `receivedDateTime ge max(anchor, now - 24h)`, newest first, at most 10
  (`--catchup-max`, `--catchup-window`). Ids already known before the loss
  are skipped; the rest are delivered oldest first.
- **Anchor.** `last_success_at` from the v2 state (the last time a delta round
  reached its deltaLink). On the first upgrade from v1 it is the **mtime of
  the v1 file** (v0.2.x rewrote it on every successful poll). On a corrupt v2
  file it is that file's mtime.
- **48 h rule.** If the anchor is older than 48 h (`--catchup-max-age`), there
  is no catch-up: a mailbox that was dead for days comes back "from now"
  instead of flooding the agent with old mail.
- **Large mailboxes.** A prime is sliced by `--max-pages-per-poll` and
  `--poll-deadline`; the next poll continues from the saved page.
- **Throttling.** On `429`/`503` the `Retry-After` header is honoured: the
  mailbox is skipped until it expires.

## State v2

`~/.config/m365cli/watch/<mailbox>__<folder>.v2.json`, mode 600:

| Field | Meaning |
| --- | --- |
| `version` | `2` |
| `delta_link` / `next_link` | Cursor of the last completed round / resume point of the round in progress |
| `priming`, `prime_started_at`, `floor` | Prime in progress; floor for moved-in old mail |
| `seen` | Recent ids, append order; the newest 2000 are kept |
| `pending` | Found but not yet delivered: id, `found_at`, attempts, next attempt |
| `catch_up` | Scheduled catch-up: anchor and the ids known before the loss |
| `last_success_at`, `last_round_completed_at`, `primed_at` | In-sync time, last steady round, last prime |
| `error_class`, `error_count`, `last_error_at` | `sync_state_lost` / `throttled` / `auth` / `transient` / `other`, reset on success |
| `v1_seen_mtime` | mtime of the v1 file whose seen ids were merged |

The file is rewritten on every successful poll, so its mtime is a liveness
signal. A file that is unreadable, truncated, of another version or without a
cursor is moved aside (`*.v2.json.corrupt-<unix>`) and the mailbox is
re-primed (catch-up anchored on that file's mtime) instead of crash-looping.

## Upgrade and rollback

- **v1 → v2.** On the first run with a v1 file and no v2 file, v2 starts a fresh
  IDs-only prime (the v1 token carries the old body `$select` and is not
  reused), seeds `seen` from the v1 seen ids and anchors the catch-up on the
  v1 file's mtime (48 h rule applies). Mailboxes whose v1 cursor has been dead
  for longer than 48 h resume from now.
- **The v1 file is never modified, renamed or deleted.** Rolling back to
  v0.2.x finds its cursor exactly where it was.
- **Forward again after a rollback.** If the v1 file is newer than the one v2
  last merged, v0.2.x ran in between: its seen ids are merged into v2 and
  removed from `pending`, so mail it delivered is not delivered twice.
- Monitoring that reads cursor ages from `watch/*.json` must prefer the
  `.v2.json` file when it exists; the v1 file's mtime freezes after the upgrade.

## Logging

- Every line carries an ISO-8601 UTC timestamp. Error lines keep the
  `watch <mailbox>: ...` prefix; informational lines start with
  `m365 mail watch: ...`.
- **No delta/next link, token or message body is ever logged.** URL query
  strings are replaced by an 8-hex-digit hash (`.../messages/delta?#3fa2c1d0`)
  so the same link can still be correlated; bearer tokens and stray
  `$deltatoken=`/`$skiptoken=` values are redacted, including inside
  transport errors.
- Identical error lines are logged at most once per minute per mailbox; the
  next line reports how many were suppressed.
