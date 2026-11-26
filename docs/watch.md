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
| `--catchup-max` | `10` | After a (re-)prime, deliver at most this many recent messages. `0` = no catch-up. |
| `--catchup-window` | `24h` | The catch-up never reaches further back than this, and **no mail received earlier is ever delivered** (dropped as `too_old`), even with a still-valid cursor after a long stop. |
| `--catchup-max-age` | `48h` | If the last good state is older than this, nothing older than the prime start is caught up: the mailbox resumes "from now". |
| `--pending-max-age` | `24h` | An undelivered message is dropped (logged) once it has waited this long. Failed attempts are not counted; only age. |
| `--max-posts-per-poll` | `20` | Hook POSTs (one message each) per mailbox per poll; the rest waits for the next poll. Must be ≥ 1. Each POST wakes the agent for one turn, so this is also the burst of agent turns (cost, concurrency) a backlog can cause per poll; the fleet may lower it. |
| `--max-pages-per-poll` | `500` | Delta pages per mailbox per poll (200 ids each). A larger prime continues on the next poll. |
| `--poll-deadline` | `3m` | Soft time budget per mailbox per poll for delta paging and delivery, checked between requests. A single in-flight request can overrun it by up to its own timeout (Graph and hook: 30 s), so one mailbox can hold the loop for roughly deadline + 30 s at most. |

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
`allowed_mailboxes` permits; otherwise the watcher refuses to start (other
commands are not affected by a `watch_mailboxes` typo). Matching is
case-insensitive and duplicates collapse. An entry that matches a concrete
`allowed_mailboxes` entry uses that entry's spelling, so the state file name
stays the one `--all` (and v0.2.x) always used; an entry allowed only via a
glob is lower-cased.

**Old binaries (v0.2.x) ignore `watch_mailboxes`:** after a rollback `--all`
watches every allowed mailbox again.

### Off switch

`~/.config/m365cli/watch.disabled` is honoured by the supervisor that starts
the watcher, not by `m365` itself; nothing here changes that contract.

### One watcher per mailbox

The engine holds an exclusive lock (`watch/<mailbox>__<folder>.v2.lock`,
`flock`) for every mailbox it polls. A second watcher on the same state
directory gets `another watcher holds ...` for that mailbox instead of racing
on the state file. The OS releases the lock when the process exits.

## Payload

POSTed as JSON (gog-compatible, unchanged), **exactly one message per POST**:

```json
{
  "source": "m365",
  "account": "agent@contoso.com",
  "messages": [
    { "id": "...", "threadId": "...", "from": "...", "to": "...",
      "subject": "...", "date": "...", "snippet": "...",
      "body": "...", "bodyTruncated": false, "labels": ["INBOX", "UNREAD"] }
  ]
}
```

`body` is present only with `--include-body`. Receivers that render only
`messages[0]` (as the fleet hook template does) therefore see every message.
`deletedMessageIds` appears only with `--include-deleted` (v1 engine, which
still batches per round).

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
3. **Per-message fetch, one message per POST.** Pending ids are delivered
   oldest first: `GET messages/{id}?$select=<production fields>` (body only
   with `--include-body`), then one POST with that one message. A `404`
   (moved or deleted meanwhile) is skipped, logged and counted as
   `fetch_404`. A failed fetch (`429`, `5xx`, timeout or connection error)
   backs off that message and stops this poll's deliveries; the rest stays
   pending. A `Retry-After` on the fetch backs off the whole mailbox.
4. **Delivery never holds the cursor.** If the hook fails, the cursor has
   already advanced.
   - **Hook `5xx` or connection error** (gateway down): delivery stops for
     this poll and the queue backs off (30 s doubling to 5 min). Nothing newer
     overtakes the failed message.
   - **Hook `4xx`** other than 401/403/404/429: this message is rejected. It is
     retried with the same backoff and dropped as `rejected` after 5
     rejections, so it cannot block newer mail for 24 h. 401/403/404/429 mean
     auth or config and keep blocking like a `5xx`.
   - Ids stay in `pending` until delivered or until they are older than
     `--pending-max-age` (24 h), then they are dropped as `expired_24h`. At
     most 500 ids are kept pending; the oldest are dropped first as
     `pending_cap`.
   - Every drop is logged and counted (see Monitoring).
5. **Second tick = 0 POSTs.** After a delivery, a restart or a re-prime, a
   poll with no new mail sends nothing.
6. **Age guard and floor.** Mail received before `now - --catchup-window`
   (24 h) is never delivered, whatever path found it. This covers a still-valid
   cursor after the watcher was stopped for days. Old mail moved or restored
   into the folder shows up as "created": mail received before the floor
   (prime start minus 10 min) is not delivered either. Both are dropped as
   `too_old`.

### First run, token loss and re-prime

- **Prime.** Every prime is IDs-only: existing ids are marked seen, nothing
  from the prime itself is delivered. Memory stays flat: ids are streamed page
  by page and the seen list is capped (a 35k mailbox primed in 55 s at 14 MB
  RSS in the measurement). A prime is sliced by `--max-pages-per-poll` and
  `--poll-deadline`; the next poll continues from the saved page.
- **Lost cursor.** Re-prime automatically, in the same poll, when a stored
  delta link answers:
  - any `410 Gone` (whatever the body: JSON, empty or HTML);
  - any 4xx whose `error.code` is `syncStateNotFound`, `resyncRequired` or
    `syncStateInvalid` (case-insensitive; the docs mention `400` as well);
  - **backstop:** the same stored link has answered non-transient 4xx (not
    401, 403 or 429) for **at least 30 min and at least 3 polls**. The first
    failure time is persisted (`stuck_since`), and any success resets it. A
    short `404` burst therefore keeps a healthy cursor. A deleted mailbox
    re-primes at most once (the fresh prime has no stored link to count). The
    log line is `stored delta link has answered HTTP <status> for <duration>`.
  A re-prime is attempted at most once per poll.
- **Catch-up after every prime.** Once the prime has its deltaLink, one list
  query fetches mail received since the **anchor**:
  `receivedDateTime ge max(anchor, now - 24h)`, newest first, at most 10
  (`--catchup-max`, `--catchup-window`). Ids known before the prime are
  skipped; the rest are delivered oldest first, one per POST.
- **Anchor.**
  - Normally `last_success_at` from the v2 state, i.e. the last time a delta
    round reached its deltaLink.
  - On the first upgrade from v1, the **mtime of the v1 file**. v0.2.x
    rewrote that file on every successful poll.
  - On a corrupt v2 file, that file's mtime.
  - On a first run without any state, the **prime start**. That way mail
    arriving while the prime pages is not lost.
- **48 h rule.** If the anchor is older than 48 h (`--catchup-max-age`), the
  catch-up is anchored at the prime start instead. A mailbox that was dead for
  days comes back "from now" and doesn't flood the agent with old mail.
- **48 h rule at prime completion.** The rule is checked again when the prime
  completes. If the catch-up anchor (or the prime start) is then older than
  48 h, the prime stalled for days, e.g. the watcher was down mid-prime. In
  that case:
  - there is no catch-up;
  - the floor moves to the completion time, so mail from before it isn't
    delivered (counted as `too_old`);
  - the mailbox resumes from the moment the prime completed.
- **Throttling.** On `429`/`503` the `Retry-After` header is honoured (capped
  at 10 min): the mailbox is skipped until it expires.

## State v2

`~/.config/m365cli/watch/<mailbox>__<folder>.v2.json`, mode 600:

| Field | Meaning |
| --- | --- |
| `version` | `2` |
| `delta_link` / `next_link` | Cursor of the last completed round / resume point of the round in progress |
| `priming`, `prime_started_at`, `floor` | Prime in progress; floor for moved-in old mail |
| `seen` | Recent ids, append order; the newest 2000 are kept |
| `pending` | Found but not yet delivered: id, `found_at`, attempts, per-message fetch backoff |
| `catch_up` | Scheduled catch-up: anchor and the ids known before the prime |
| `last_success_at`, `last_round_completed_at`, `primed_at` | In-sync time, last steady round, last prime |
| `error_class`, `error_count`, `last_error_at` | `sync_state_lost` / `throttled` / `auth` / `transient` / `other`; reset on success |
| `stuck_link`, `stuck_since`, `stuck_count` | Backstop: stored link (hash) answering non-transient 4xx since when, how many polls |
| `deliver_failures`, `deliver_not_before` | Consecutive hook failures and the queue's backoff horizon |
| `dropped_total`, `last_dropped_at`, `last_dropped_count`, `dropped_by_reason` | Dropped mail (see Monitoring) |
| `v1_seen_mtime` | mtime of the v1 file whose seen ids were merged |

A file that is unreadable, truncated, of another version or without a cursor
is moved aside (`*.v2.json.corrupt-<unix>`) and the mailbox is re-primed
(catch-up anchored on that file's mtime) instead of crash-looping.

## Monitoring

**The file's mtime is not a liveness signal.** The file is also rewritten on
failed polls (error counters, backoff). Read the JSON instead:

| Read | Healthy | Alarm when |
| --- | --- | --- |
| `last_success_at` | a few minutes old at most | **older than 15 min**. That is longer than a `Retry-After` backoff (≤ 10 min) and a sliced prime (one slice ≤ 3 min per poll). |
| `error_class`, `error_count` | empty / 0 | `error_count` keeps growing; `auth` points at permissions |
| `priming` | `false` | `true` for longer than a few polls (stuck prime) |
| `len(pending)` | 0 | > 0 for long |
| oldest `pending[].found_at` | — | older than a few minutes (hook down); the message is dropped at 24 h |
| `dropped_total` / `last_dropped_at` | unchanged | grows: mail was found but never delivered |

**Dropped mail.** Every message that was found but will never be delivered is
counted:

```json
"dropped_total": 7,
"last_dropped_at": "2026-11-26T13:05:00Z",
"last_dropped_count": 2,
"dropped_by_reason": {"expired_24h": 3, "too_old": 2, "rejected": 1, "fetch_404": 1}
```

- `dropped_total` only grows.
- `last_dropped_count` is the number dropped in the poll at `last_dropped_at`.
- The keys are omitted while they are zero or empty (a fresh state has none),
  and `dropped_by_reason` lists only the reasons that occurred.

| Reason | Meaning | Alarm? |
| --- | --- | --- |
| `expired_24h` | pending longer than `--pending-max-age` (hook down for a day) | yes |
| `rejected` | the hook answered 4xx five times for this message | yes |
| `pending_cap` | more than 500 undelivered; oldest dropped | yes |
| `too_old` | received before `now - 24h` or before the floor (long stop, stalled prime, moved-in old mail) | informational, by design |
| `fetch_404` | moved or deleted before it could be delivered | informational |

The v1 file (`*.json` without `.v2`) is no longer written after the upgrade.
A monitor must read the `.v2.json` file when it exists.

## Upgrade, rollback and reset

- **v1 → v2.** On the first run with a v1 file and no v2 file, v2 starts a fresh
  IDs-only prime (the v1 token carries the old body `$select` and is not
  reused), seeds `seen` from the v1 seen ids and anchors the catch-up on the
  v1 file's mtime (48 h rule applies). Mailboxes whose v1 cursor has been dead
  for longer than 48 h resume from now.
- **The v1 file is never modified, renamed or deleted.**
- **Rollback is not guaranteed safe** (inferred, not yet measured). The v1
  file is intact, but its token is probably evicted by the v2 prime (several
  pages, each a new generation). In that case v0.2.x hits `410` and only
  recovers by deleting its cursor. That triggers its old **full-body** first
  run, which on a large mailbox can take hundreds of MB (OOM risk; see the
  fleet runbook on re-priming). v0.2.x also ignores `watch_mailboxes`, so its
  watch scope widens to every allowed mailbox. Plan a rollback like a re-prime.
- **Forward again after a rollback.** If the v1 file is newer than the one v2
  last merged, v0.2.x ran in between. Its seen ids are merged into v2 and
  removed from `pending`, so mail it delivered is not delivered twice.
- **Resetting a mailbox.** Stop the watcher, then move the `.v2.json` file
  aside. On the next start the mailbox re-primes.
  - If the v1 file is still there, this **re-runs the v1 migration**. Its
    catch-up is anchored on the v1 mtime, so within 48 h of the upgrade
    messages that v2 already delivered can be delivered again (up to the
    catch-up cap).
  - For a reset "from now", move the v1 file aside too. A first run with no
    state only catches up mail that arrives during the prime.
  - Never delete files while the watcher runs: it holds the lock and rewrites
    the state.

## Logging

- Every line carries an ISO-8601 UTC timestamp. Error lines keep the
  `watch <mailbox>: ...` prefix; informational lines start with
  `m365 mail watch: ...`.
- **No delta/next link, token, message id or message body is ever logged.**
  URL query strings are replaced by an 8-hex-digit hash
  (`.../messages/delta?#3fa2c1d0`) so the same link can still be correlated.
  Message ids appear only as a hash (`#1a2b3c4d`). Bearer tokens and stray
  `$deltatoken=`/`$skiptoken=` values are redacted, including inside
  transport errors.
- Identical error lines are logged at most once per minute per mailbox; the
  next line reports how many were suppressed.
