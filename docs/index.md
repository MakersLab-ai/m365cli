# m365 documentation

`m365` is the [`gog`](https://gogcli.sh)-style CLI for Microsoft 365 / Microsoft
Graph — app-only (no user login), scoped by hard allowlists, with stable JSON
output for terminals, scripts, CI, and coding agents. Made by
[makerslab.ai](https://makerslab.ai).

## Start here

- **[README](../README.md)** — overview, install, the command index, and the
  security model.
- **[Azure setup](azure-setup.md)** — the administrator guide: app registration,
  certificate, RBAC for Applications (mail/calendar/contacts) and
  `Sites.Selected` (drive/SharePoint, incl. per-OneDrive grants), and the
  `config.toml` mapping. Also available in German:
  [azure-setup.de.md](azure-setup.de.md).
- **[On-premise Exchange (EWS)](ews-setup.md)** — the `backend = "ews"` path for
  local Exchange servers: NTLM service account, ExchangeImpersonation, config.
- **[Watching mail](watch.md)** — `m365 mail watch poll`: delta-poll mailboxes
  and forward new mail to a webhook (usage, payload, reliability contract).
- **[Agent skill](../.agents/skills/m365/SKILL.md)** — how a coding agent should
  drive `m365` (fast path, safety rules, common reads/writes).

## Command domains

| Domain | Verbs | Scope |
| --- | --- | --- |
| `doctor` | (`--live`) | config + certificate + token health |
| `mail` | list, read, search, send, draft, reply, attachments, get-attachment, watch poll | `allowed_mailboxes` |
| `calendar` | list, get, create, update, delete, freebusy, find-times | `allowed_mailboxes` |
| `contacts` | list, get, add | `allowed_mailboxes` |
| `drive` | ls, search, get, download, upload | `allowed_mailboxes` (OneDrive) |
| `sp` | sites, list, items, download | `allowed_sites` (SharePoint) |

Run `m365 <domain> --help` or `m365 <domain> <verb> --help` for flags.

## Key concepts

- **App-only auth.** Certificate-based client credentials; no browser, no
  consent flow. See [Azure setup](azure-setup.md).
- **Fail-closed allowlists.** Mailboxes (`allowed_mailboxes`) and sites
  (`allowed_sites`) are matched exactly or by glob (`*@contoso.com`); an empty
  list denies everything. Enforced before any network call.
- **Send guardrail.** Mail to a recipient outside `send_allow` becomes a draft
  for human review, unless `send_unrestricted` is set (which `doctor` flags).
- **Output contract.** `--json` writes a stable envelope to stdout; human hints
  go to stderr.
