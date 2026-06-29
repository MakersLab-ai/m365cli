# On-premise Exchange (EWS) backend

For organizations whose mail lives on a **local Exchange server** rather than in
Microsoft 365, `m365` can talk **EWS** (Exchange Web Services) instead of the
Graph cloud API. Select it with `backend = "ews"` in `config.toml`.

> **Status: preview.** Implemented: `mail list`, `mail read`, `mail search`,
> `mail send`, `mail draft`. Not yet on EWS: `mail reply`, attachments, calendar,
> contacts, drive/SharePoint, and `mail watch` — these return `operation not
> supported by this backend`. The path is validated against fixtures (httptest),
> not yet against a live server.

## How it differs from the cloud (Graph) backend

| | Graph (cloud) | EWS (on-premise) |
| --- | --- | --- |
| Auth | app-only certificate (no secret) | **NTLM** with a domain **service account** (password) |
| Mailbox scoping | RBAC for Applications | **ExchangeImpersonation** (service account impersonates each mailbox) |
| Reachability | `graph.microsoft.com` | your EWS endpoint must be reachable (published externally **or** via VPN) |

The `allowed_mailboxes` allowlist is enforced exactly as on the cloud path — an
out-of-scope mailbox is refused before any network call.

## Configuration

```toml
backend           = "ews"
ews_url           = "https://mail.example.com/EWS/Exchange.asmx"
ews_user          = "EXAMPLE\\svc-agent"   # DOMAIN\user, or a UPN svc-agent@example.com
ews_password_file = "/etc/m365/ews.pass"   # 0600 file holding ONLY the password
default_mailbox   = "agent@example.com"
allowed_mailboxes = ["agent@example.com", "*@example.com"]
```

The password is read from `ews_password_file` (a `0600` file) — never put it in
`config.toml` or on the command line.

## What the Exchange admin must provide

1. A **domain service account** for the agent.
2. The **ApplicationImpersonation** RBAC role for that account, scoped to the
   mailboxes the agent may read (this is what lets it impersonate them via EWS).
3. An EWS endpoint reachable from where the agent runs (published or via VPN).

## Verify

```sh
m365 --config config.toml mail list --max 5
m365 --config config.toml mail read <message-id>
m365 --config config.toml mail search "subject:invoice"
m365 --config config.toml mail send --to user@example.com --subject Hi --body-file ./msg.txt
```

`send` honours the same `send_allow` guardrail as the cloud backend: a recipient
outside the allowlist downgrades the message to a draft for review.

A `401` error means the service account credentials or impersonation rights are
wrong; an EWS `ResponseCode` (e.g. `ErrorImpersonateUserDenied`) points at the
RBAC scoping.
