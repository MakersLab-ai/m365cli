# Changelog

## v0.3.0-rc (unreleased)

### mail watch: state v2 (Graph)

The Graph watcher is rebuilt around measured Graph behaviour (one delta
sync-state generation per page, only a few kept). It no longer kills its own
cursor when the hook fails. See [docs/watch.md](docs/watch.md).

- **State v2** (`watch/<mailbox>__<folder>.v2.json`): atomic writes (temp,
  fsync, rename, dir fsync), schema version, cursor or resume link, capped seen
  list (newest kept), pending ids, success/prime timestamps, error class and
  counter. Corrupt or truncated files are moved aside and re-primed.
- **Cursor persisted after every delta page.** A 5xx or crash mid-pagination
  resumes from the saved page.
- **IDs-only delta** (`changeType=created&$select=id`,
  `Prefer: odata.maxpagesize=200`); `@removed` ignored. Messages are fetched
  one by one for delivery with the production field list; `404` is skipped.
- **One message per hook POST**, oldest first, up to 20 per poll (receivers
  that render only `messages[0]` see every mail).
- **Delivery no longer holds the cursor.** Undelivered ids stay pending, the
  queue backs off on hook failures (order kept), and ids are dropped with a
  log line only by age (24 h). A message the hook rejects with 4xx five times
  is dropped as `rejected` instead of blocking the queue.
- **Age guard:** mail received more than 24 h ago is never delivered (a valid
  cursor after a long stop does not replay days of mail); the 48 h rule is
  also applied when a stalled prime completes.
- **Dropped-mail counters** in the state file: `dropped_total`,
  `last_dropped_at`, `last_dropped_count`, `dropped_by_reason`.
- Message fetch `5xx`/timeouts stop the poll's deliveries; the poll deadline
  bounds delivery even when nothing is posted.
- **Self-healing on token loss.** Any 410, or a 4xx with
  `syncStateNotFound`/`resyncRequired`/`syncStateInvalid`, triggers an
  automatic, memory-bounded IDs-only re-prime plus a bounded catch-up (10
  messages, 24 h; anchored at the prime start if the last good state is older
  than 48 h). Backstop: a stored link answering non-transient 4xx (not
  401/403/429) for 30 min and at least 3 polls re-primes too. Every prime is followed by a catch-up, so mail
  arriving during a first-run prime is delivered.
- **Single-instance lock** (flock) per mailbox; `Retry-After` capped at 10 min.
- **v1 → v2 migration** without touching the v1 file; seen ids are seeded
  from v1, catch-up anchored on the v1 file's mtime. A rollback is **not**
  guaranteed safe: the v1 token is probably evicted by the v2 prime, so
  v0.2.x may need its full-body re-prime (OOM risk on large mailboxes); see
  docs/watch.md.
- **`watch_mailboxes`** in `config.toml` narrows `poll --all` to a subset of
  `allowed_mailboxes` (default: all concrete entries, as before);
  case-insensitive, deduplicated.
- **Logging:** ISO timestamps on every line, links/tokens/bodies redacted,
  identical errors rate-limited to one line per minute.
- **Graph client:** typed `*graph.APIError` (status, `error.code`, parsed
  `Retry-After`); errors never include the request query string.

Unchanged: the hook payload shape (byte-compatible per message), the EWS watcher, and
`--include-changed` / `--include-deleted` (both keep using the v1 engine).
