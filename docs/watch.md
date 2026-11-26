# Watching mail (`m365 mail watch poll`)

`m365 mail watch poll` is a long-running loop that delta-polls one or more
mailboxes and forwards new (and optionally changed/deleted) messages to a
webhook. App-only, no public inbound endpoint required.

## Usage

```bash
m365 mail watch poll \
  --mailbox agent@contoso.com \      # repeatable; or --all for all concrete allowed_mailboxes
  --folder inbox \                   # default: inbox
  --interval 30s \                   # default: 30s
  --hook-url https://example/hook \  # required
  --hook-token <bearer> \            # optional
  --include-body --max-bytes 20000 \ # body is opt-in
  --include-changed --include-deleted
```

Each mailbox is checked against `allowed_mailboxes` before any request. The delta
cursor and a bounded seen-id set are stored per `(mailbox, folder)` at
`~/.config/m365cli/watch/` (mode 600).

## Payload

POSTed as JSON (gog-compatible):

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
`--include-deleted`.

## Reliability

- **First run primes the cursor** and delivers nothing — existing mail is not
  replayed as "new". On Graph, priming only covers mail received in the last
  hour (`$filter=receivedDateTime ge <floor>`), and only ids are kept in memory,
  so a large inbox primes in seconds instead of paging every message with its
  body. The floor is stored with the cursor; mail received before it is never
  delivered as new, even if Graph later reports a change on it (e.g. read flag).
- **Expired cursor recovers itself:** when Graph answers `410 Gone` /
  `SyncStateNotFound` (the delta token's sync state is gone), the watcher drops
  the cursor and primes a fresh one in the same poll. Mail received while the
  cursor was dead is **not** delivered (it stays in the mailbox), and a hint goes
  to stderr. Other errors leave the cursor untouched and are retried.
- **Delivery-before-cursor-advance:** the cursor advances only after the webhook
  returns 2xx. A failing hook is retried on the next interval, so wake-ups are not
  lost.
- **At-least-once:** a crash between a successful POST and the cursor write causes
  redelivery. Receivers must tolerate duplicate message ids.

## Keeping it running

`watch poll` is a foreground loop. Run it under whatever process supervisor you
use (launchd, systemd, a gateway that supervises child processes, etc.) and point
`--hook-url` at your receiver.
