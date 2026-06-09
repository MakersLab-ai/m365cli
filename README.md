# m365

**The [`gog`](https://gogcli.sh)-style CLI alternative for Microsoft 365.**
A single static Go binary for Microsoft 365 / Microsoft Graph — mail, calendar,
contacts, OneDrive, and SharePoint — built for terminals, shell scripts, CI, and
coding agents.

Made by [makerslab.ai](https://makerslab.ai).

```bash
m365 mail list --mailbox agent@contoso.com --json
m365 calendar create --subject "Sync" --start 2026-06-10T10:00:00 --end 2026-06-10T10:30:00
m365 drive ls --json
```

## What makes it different

- **App-only, no login.** Authentication is certificate-based client credentials
  (Microsoft Graph app-only). There is no browser consent flow and no user
  session — an administrator sets up one Azure app once. Ideal for unattended
  agents and CI.
- **Hard allowlists, fail-closed.** Every mailbox and every SharePoint site the
  binary may touch is listed in config. Anything not listed is refused **before**
  any network call. An empty list denies everything.
- **Send guardrail.** Mail to a recipient outside your `send_allow` list is never
  sent silently — it becomes a **draft** for human review. You opt out
  explicitly, and `doctor` warns when you do.
- **Agent-friendly output.** `--json` emits a stable envelope on stdout; human
  hints and progress go to stderr, so pipelines stay parseable.

## Install

```bash
# Install the latest from source (produces the `m365` binary on your PATH):
go install github.com/MakersLab-ai/m365cli/cmd/m365@latest

# Or build from a checkout:
git clone https://github.com/MakersLab-ai/m365cli
cd m365cli && go build -o bin/m365 ./cmd/m365
```

Requires Go 1.26+.

## Setup

`m365` needs an Azure app registration (with a certificate) and access scoped to
the mailboxes/sites you intend to use. The full administrator guide —
app registration, certificate, RBAC for Applications, and `Sites.Selected` — is
in **[docs/azure-setup.md](docs/azure-setup.md)**.

Then create `~/.config/m365cli/config.toml` (mode `600`):

```toml
tenant_id  = "<directory-tenant-id>"
client_id  = "<application-client-id>"
cert_path  = "/path/to/m365-app.pem"   # certificate + private key, one PEM

default_mailbox   = "agent@contoso.com"
allowed_mailboxes = ["agent@contoso.com", "*@contoso.com"]  # exact or glob
allowed_sites     = ["contoso.sharepoint.com,*"]

send_allow        = ["*@partner.com"]   # direct external send; others → draft
# send_unrestricted = true              # disable the send guardrail (doctor warns)
```

Verify:

```bash
m365 doctor          # offline checks: config, certificate, allowlists
m365 doctor --live   # acquires a real Graph token (verifies cert + tenant)
```

## Commands

```
m365 doctor    [--live]                                     # config + token health
m365 mail      list read search send draft reply
               attachments get-attachment                   # scoped by allowed_mailboxes
m365 calendar  list get create update delete
               freebusy find-times                          # scoped by allowed_mailboxes
m365 contacts  list get add                                 # scoped by allowed_mailboxes
m365 drive     ls search get download upload                # OneDrive, scoped by allowed_mailboxes
m365 sp        sites list items download                    # SharePoint, scoped by allowed_sites
```

Run `m365 <domain> --help` or `m365 <domain> <verb> --help` for flags.

### Composing mail safely

Message bodies are read from a file (`--body-file`) rather than a flag, so there
is no shell-escaping of mail text:

```bash
m365 mail send --mailbox agent@contoso.com \
  --to person@contoso.com --subject "Update" --body-file ./body.txt

# A recipient outside send_allow → saved as a draft, not sent:
m365 mail send --to stranger@example.com --subject Hi --body-file ./body.txt
# stderr: send guardrail: [stranger@example.com] not in send_allow — saving as draft
```

## Output

- `--json` — a stable envelope on stdout: `{"ok":true,"data":…}` or
  `{"ok":false,"error":{"message":…}}`.
- Human-facing messages and progress go to **stderr**; data goes to **stdout**.

## Security model

`m365`'s allowlists are defense-in-depth **on top of** the access you grant in
Azure (RBAC for Applications for mailboxes, `Sites.Selected` for sites). Even if
those grants were too broad, `m365` refuses any mailbox or site not in your
config. Secrets (the certificate) live on disk at `cert_path` and are never
passed on the command line; the token cache is written `600`.

## Not in scope

Teams chat, full Tasks/To-Do parity, and personal Microsoft accounts are out of
scope — `m365` targets Microsoft 365 work tenants with app-only access. For
delegated, user-login scenarios on Google Workspace, see [`gog`](https://gogcli.sh).

## License

See [LICENSE](LICENSE).

---

Made by [makerslab.ai](https://makerslab.ai) — the `gog` alternative for Microsoft 365.
