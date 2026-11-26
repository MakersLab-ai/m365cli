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
- **Delivery no longer holds the cursor.** Undelivered ids stay pending and
  are retried with backoff, dropped with a log line after 24 h / 100 attempts.
- **Self-healing on token loss.** `syncStateNotFound` (any 4xx) triggers an
  automatic, memory-bounded IDs-only re-prime plus a bounded catch-up (10
  messages, 24 h; none if the last good state is older than 48 h).
- **v1 → v2 migration** without touching the v1 file (rollback-safe); seen ids
  are seeded from v1, catch-up anchored on the v1 file's mtime.
- **`watch_mailboxes`** in `config.toml` narrows `poll --all` to a subset of
  `allowed_mailboxes` (default: all concrete entries, as before).
- **Logging:** ISO timestamps on every line, links/tokens/bodies redacted,
  identical errors rate-limited to one line per minute.
- **Graph client:** typed `*graph.APIError` (status, `error.code`, parsed
  `Retry-After`); errors never include the request query string.

Unchanged: the hook payload (byte-compatible), the EWS watcher, and
`--include-changed` / `--include-deleted` (both keep using the v1 engine).
