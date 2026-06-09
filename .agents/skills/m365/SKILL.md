---
name: m365
description: "m365 CLI: app-only Microsoft 365 / Graph automation — mail, calendar, contacts, OneDrive, SharePoint. Stable JSON, hard allowlists, no user login."
---

# m365

Use `m365` to read and act on Microsoft 365 data (mail, calendar, contacts,
OneDrive, SharePoint) from the shell when you need stable JSON, scoped app-only
access, or automation without a signed-in user.

`m365` is app-only: there is **no login and no consent flow**. An administrator
has already placed a certificate and a config file; you just run commands.

## Fast Path

```bash
m365 --help
m365 doctor --json          # config + certificate + allowlist health
m365 doctor --live --json   # also acquire a real Graph token
m365 mail list --json       # uses default_mailbox
```

Prefer `--json` for parsing. Data goes to stdout; human hints/progress go to
stderr — read stdout for results.

## Safety Rules

- Do not print or echo the certificate, the token cache, or any config secret.
- `m365` is scoped by **allowlists** in config (`allowed_mailboxes`,
  `allowed_sites`). A mailbox or site that is not listed is refused — do not try
  to work around it; tell the user it is out of scope.
- **Sending mail is guarded.** `m365 mail send`/`reply` to a recipient outside
  `send_allow` does **not** send — it creates a **draft** for human review. Don't
  try to force a direct send; if the user truly wants unrestricted send, that is
  an operator config change (`send_unrestricted`), not something you set per call.
- Compose bodies with **`--body-file`**, never an inline body flag (avoids shell
  escaping and accidental content corruption).
- `calendar delete` and similar mutations are irreversible — only run them when
  the user asked for that exact change.
- Run `m365 doctor` first if anything is misconfigured; it explains what's wrong
  without touching data.

## Auth

App-only, certificate-based. Unlike a delegated CLI, there is nothing
interactive to do:

```bash
m365 doctor --live --json   # verifies cert + tenant + token acquisition
```

If `doctor --live` fails:

- `AADSTS90002 Tenant not found` → wrong `tenant_id` in config.
- cert errors (`AADSTS700027`) → the uploaded certificate doesn't match the key
  in `cert_path`, or it expired.
- token works but mailbox calls return 403 → the mailbox isn't in the app's RBAC
  scope yet (changes take up to ~2h), or it's missing from `allowed_mailboxes`.

Config lives at `~/.config/m365cli/config.toml` (or `--config <path>`); the
certificate is at the `cert_path` it points to. Do not read or print them unless
diagnosing, and never print their contents.

## Common Reads

```bash
m365 mail list --mailbox user@contoso.com --max 10 --json
m365 mail read <message-id> --json
m365 mail search 'quarterly report' --json
m365 mail attachments <message-id> --json

m365 calendar list --start 2026-06-10T00:00:00 --end 2026-06-11T00:00:00 --json
m365 calendar get <event-id> --json
m365 calendar freebusy --schedule a@contoso.com --schedule b@contoso.com \
  --start 2026-06-10T09:00:00 --end 2026-06-10T17:00:00 --json

m365 contacts list --json
m365 drive ls --path /Reports --json
m365 sp sites contoso --json          # discover site IDs
m365 sp items <site-id> --json
```

## Writes

Before any write, confirm the mailbox/site, the target id, and the exact change.
Bodies come from a file.

```bash
# Send — becomes a draft automatically if a recipient is outside send_allow:
m365 mail send --mailbox agent@contoso.com \
  --to person@contoso.com --subject "Update" --body-file ./body.txt --json

m365 mail draft --to person@contoso.com --subject "WIP" --body-file ./body.txt --json
m365 mail reply <message-id> --body-file ./reply.txt --json
m365 mail reply <message-id> --reply-all --body-file ./reply.txt --json

m365 calendar create --subject "Sync" \
  --start 2026-06-10T10:00:00 --end 2026-06-10T10:30:00 \
  --attendee a@contoso.com --body-file ./agenda.txt --json
m365 calendar update <event-id> --subject "Renamed" --json   # only given fields change
m365 calendar delete <event-id> --json

m365 contacts add --email ada@contoso.com --given Ada --surname Lovelace --json

m365 drive upload ./report.pdf --dest Reports/report.pdf --json
m365 drive download <item-id> --out ./report.pdf --json
m365 sp download <site-id> <item-id> --out ./file.docx --json
```

The send guardrail also applies to `reply`/`reply-all`: recipients are read from
the original message, and if any is outside `send_allow` the result is a
reply-draft, not a sent reply. The JSON envelope reports `sent`/`draft` and any
`blocked` recipients.

## Discovery

```bash
m365 <domain> --help          # mail | calendar | contacts | drive | sp | doctor
m365 <domain> <verb> --help   # flags for a specific verb
```

Docs and layout:

- Setup (admin): `docs/azure-setup.md`
- CLI entrypoint: `cmd/m365/`
- Command wiring: `internal/cli/`
- Auth (cert + token cache): `internal/auth/`
- Scoped Graph client (allowlist choke point): `internal/graph/`
- Domain logic + guardrails: `internal/{config,mail,calendar,contacts}/`
