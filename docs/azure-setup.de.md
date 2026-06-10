# Azure-/Microsoft-365-Einrichtung

> 🇬🇧 English version: **[azure-setup.md](azure-setup.md)**

`m365` authentifiziert sich **app-only** (Client Credentials mit Zertifikat). Es
gibt **keinen Benutzer-Login und keinen Consent-Dialog** — ein Administrator
richtet einmalig eine Entra-App-Registrierung ein und beschränkt deren Zugriff
auf bestimmte Postfächer und Sites.

Diese Anleitung richtet sich an den Administrator, der den Tenant vorbereitet.
Sie behandelt die eine Azure-App, ihr Zertifikat, die Berechtigungen und — ganz
entscheidend — wie diese Berechtigungen **eingegrenzt** werden, damit die App nur
die Postfächer und Sites anfasst, die vorgesehen sind.

> **Zwei Scoping-Modelle, mit Absicht.** Postfachdaten (Mail, Kalender,
> Kontakte) werden mit **RBAC for Applications** in Exchange Online eingegrenzt.
> Dateien/SharePoint werden mit **`Sites.Selected`** in Microsoft Graph
> eingegrenzt. Beide werden unterschiedlich konfiguriert — siehe die jeweiligen
> Abschnitte unten.

---

## 1. Anwendung registrieren (Microsoft Entra ID)

1. Entra Admin Center → **Identity → Applications → App registrations → New
   registration**.
2. Namen vergeben (z. B. `m365cli`), Single-Tenant, keine Redirect-URI.
3. **Application (client) ID** und **Directory (tenant) ID** vom Overview-Blade
   notieren — beide kommen in die `config.toml`.

## 2. Zertifikat hinterlegen

App-only-Auth verwendet eine Zertifikats-Assertion (gegenüber einem Client
Secret zu bevorzugen: langlebiger, kein Bearer-Geheimnis auf der Platte).

Selbstsigniertes Zertifikat erzeugen (oder eines aus der eigenen PKI verwenden):

```bash
# Privater Schlüssel + öffentliches Zertifikat, 365 Tage gültig
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout m365-key.pem -out m365-cert.pem \
  -days 365 -subj "/CN=m365cli"

# m365 erwartet EINE PEM-Datei mit Zertifikat UND privatem Schlüssel:
cat m365-cert.pem m365-key.pem > m365-app.pem
chmod 600 m365-app.pem
```

Nur das **öffentliche** Zertifikat hochladen:

- App-Registrierung → **Certificates & secrets → Certificates → Upload
  certificate** → `m365-cert.pem` hochladen.

`m365-app.pem` (Zertifikat + Schlüssel) verbleibt auf der Maschine, die `m365`
ausführt, Modus `600`. Den privaten Schlüssel **niemals** hochladen oder
committen.

## 3. Postfachzugriff eingrenzen — RBAC for Applications

So werden Mail, Kalender und Kontakte auf bestimmte Postfächer beschränkt.

> ⚠️ **Nicht zusätzlich die tenant-weite Berechtigung in Entra ID vergeben.**
> RBAC-Zuweisungen sind *unabhängig und additiv* zu Entra-App-Berechtigungen.
> Wer `Mail.ReadWrite` tenant-weit in Entra vergibt **und** per RBAC eingrenzt,
> erhält in Summe faktisch uneingeschränkten (tenant-weiten) Zugriff. Für
> eingegrenzten Zugriff die Berechtigung **ausschließlich** über RBAC (unten)
> vergeben — die **API permissions** der App in Entra für Postfach-Scopes
> **leer lassen**.

Als Exchange-Admin verbinden (Mitglied von **Organization Management**):

```powershell
Connect-ExchangeOnline
```

**a) Verweis auf den Service Principal der App anlegen.** IDs von der
Entra-Seite **Enterprise applications** verwenden (App ID + die Object ID der
*Enterprise App* — nicht die Object ID der App-Registrierung):

```powershell
New-ServicePrincipal -AppId <client-id> -ObjectId <enterprise-app-object-id> -DisplayName "m365cli"
```

**b) Resource Scope definieren** (welche Postfächer). Am einfachsten: eine
mail-aktivierte Sicherheitsgruppe, deren Mitglieder die erlaubten Postfächer
sind, dann ein Management Scope, der über den Distinguished Name darauf zeigt:

```powershell
$dn = (Get-Group "m365cli-mailboxes").DistinguishedName
New-ManagementScope -Name "m365cli scope" -RecipientRestrictionFilter "MemberOfGroup -eq '$dn'"
```

(Alternativ lässt sich über jede [Recipient-Filter-Eigenschaft](https://learn.microsoft.com/en-us/powershell/exchange/recipientfilter-properties)
eingrenzen, oder über eine Microsoft-Entra-**Administrative Unit** via
`-RecipientAdministrativeUnitScope`.)

**c) Die Anwendungsrollen zuweisen, eingegrenzt.** Für Phase 1 (Mail + Kalender):

```powershell
New-ManagementRoleAssignment -App <enterprise-app-object-id> -Role "Application Mail.ReadWrite"     -CustomResourceScope "m365cli scope"
New-ManagementRoleAssignment -App <enterprise-app-object-id> -Role "Application Mail.Send"          -CustomResourceScope "m365cli scope"
New-ManagementRoleAssignment -App <enterprise-app-object-id> -Role "Application Calendars.ReadWrite" -CustomResourceScope "m365cli scope"
# Phase 2 (Kontakte):
New-ManagementRoleAssignment -App <enterprise-app-object-id> -Role "Application Contacts.ReadWrite"  -CustomResourceScope "m365cli scope"
```

> `Application Mail.ReadWrite` umfasst **kein** Senden — `Application Mail.Send`
> ist separat. (`Application Mail Full Access` bündelt beides.)

**d) Scope verifizieren.** Dieser Befehl umgeht den Berechtigungs-Cache:

```powershell
Test-ServicePrincipalAuthorization -Identity "m365cli" -Resource allowed@contoso.com | Format-Table
# InScope = True für ein erlaubtes Postfach, False für eines außerhalb des Scopes.
```

> **Cache:** RBAC-Änderungen brauchen **30 Minuten bis 2 Stunden**, bis sie für
> Live-Aufrufe greifen (das Test-Cmdlet umgeht das). Wenn `m365`-Aufrufe direkt
> nach einer Änderung fehlschlagen: warten und erneut versuchen.

## 4. Dateizugriff eingrenzen — `Sites.Selected` (Phase 2)

Dateien (SharePoint **und** OneDrive) verwenden **kein** Exchange-RBAC.
Stattdessen:

> ⚠️ **`Files.ReadWrite.All` / `Files.Read.All` vermeiden.** Diese gelten
> *tenant-weit*: Die App erreicht das OneDrive **jedes** Users und **jede**
> Site, und Azure bietet **keine Möglichkeit, sie auf einzelne Drives
> einzuschränken**. `Sites.Selected` ist die eingegrenzte Alternative —
> Default-Deny, mit expliziten Freigaben pro Site/Drive.

1. App-Registrierung → **API permissions → Add → Microsoft Graph → Application
   permissions** → **`Sites.Selected`** hinzufügen → **Grant admin consent**.
   Mit diesem Consent allein hat die App Zugriff auf **gar nichts**, bis unten
   Sites einzeln freigegeben werden.
2. Der App Zugriff auf jede Ziel-Site einzeln gewähren (als Admin, via Graph):

   ```http
   POST https://graph.microsoft.com/v1.0/sites/{site-id}/permissions
   {
     "roles": ["read"],                          // oder "write"
     "grantedToIdentities": [{
       "application": { "id": "<client-id>", "displayName": "m365cli" }
     }]
   }
   ```

Erreichbar sind nur Sites, die in `allowed_sites` (Config) stehen **und** hier
freigegeben wurden.

### Einzelne OneDrives eingrenzen

Das OneDrive eines Users ist technisch eine **persönliche SharePoint-Site**
(`contoso-my.sharepoint.com/personal/user_contoso_com`) — dieselbe
`Sites.Selected`-Freigabe funktioniert daher pro OneDrive: Die App erhält
Zugriff auf genau die Drives, die freigegeben werden, und auf nichts sonst.

1. Site-ID des User-OneDrives ermitteln (als Admin mit ausreichenden Rechten,
   z. B. via Graph Explorer):

   ```http
   GET https://graph.microsoft.com/v1.0/users/{user}/drive?$select=sharePointIds
   # → sharePointIds.siteId ist die {site-id} für den Permissions-Aufruf
   ```

2. Zugriff mit demselben `POST /sites/{site-id}/permissions`-Aufruf wie oben
   gewähren.

Hinweise:

- **Eine Freigabe pro Drive.** Es gibt keine Gruppen-/Sammelfreigabe („alle
  OneDrives der Abteilung X") — jedes Drive ist eine einzelne Freigabe. Die
  Aufrufe lassen sich für eine User-Liste leicht skripten.
- **Feiner als ein ganzes Drive:** Die neueren Anwendungsberechtigungen
  `Files.SelectedOperations.Selected` und `Lists.SelectedOperations.Selected`
  erlauben Freigaben bis hinunter auf einzelne **Ordner, Dateien oder
  Dokumentbibliotheken** statt einer ganzen Site/eines ganzen Drives, über die
  entsprechenden `permissions`-Endpunkte.
- **Keine Freigabe → kein Zugriff.** Ohne explizite Freigabe schlägt jeder
  `m365 drive`-Aufruf mit 403 von Graph fehl — unabhängig von
  `allowed_mailboxes`.

## 5. `config.toml` schreiben

`~/.config/m365cli/config.toml` (Modus `600`):

```toml
tenant_id  = "<directory-tenant-id>"
client_id  = "<application-client-id>"
cert_path  = "/path/to/m365-app.pem"   # Zertifikat + privater Schlüssel, eine PEM, Modus 600

default_mailbox   = "agent@contoso.com"
allowed_mailboxes = ["agent@contoso.com", "*@contoso.com"]  # exakt oder Glob
allowed_sites     = ["contoso.sharepoint.com,*"]            # Phase 2

# Direkter externer Versand ist standardmäßig AUS: nicht gelistete Empfänger werden Entwürfe.
send_allow        = ["*@partner.com"]
# send_unrestricted = true   # deaktiviert den Versand-Guardrail (doctor warnt)
```

Die Allowlists von `m365` sind ein **zusätzlicher** Schutz oberhalb von
RBAC/`Sites.Selected`: Selbst wenn RBAC zu breit konfiguriert wäre, verweigert
`m365` jedes Postfach und jede Site, die nicht in der Config steht (fail-closed
— eine leere Liste verbietet alles).

## 6. Verifizieren

```bash
m365 doctor          # offline: Config, Zertifikat vorhanden, Allowlists
m365 doctor --live   # holt ein echtes Graph-Token (verifiziert Zertifikat + Tenant)
m365 mail list --mailbox agent@contoso.com --json
```

Ein grünes `doctor --live` plus ein erfolgreiches `mail list` gegen ein
erlaubtes Postfach bestätigt die komplette Kette.

---

## Berechtigungs-Referenz

| Bereich | Graph-Berechtigung | Vergabe / Eingrenzung |
| --- | --- | --- |
| Mail lesen/schreiben | `Mail.ReadWrite` | RBAC-Rolle `Application Mail.ReadWrite`, eingegrenzt |
| Mail senden | `Mail.Send` | RBAC-Rolle `Application Mail.Send`, eingegrenzt |
| Kalender | `Calendars.ReadWrite` | RBAC-Rolle `Application Calendars.ReadWrite`, eingegrenzt |
| Kontakte | `Contacts.ReadWrite` | RBAC-Rolle `Application Contacts.ReadWrite`, eingegrenzt |
| SharePoint/OneDrive | `Sites.Selected` | Entra-Consent + Freigabe pro Site/Drive (ein OneDrive ist eine persönliche Site) |
| Einzelne Ordner/Dateien (optional) | `Files.SelectedOperations.Selected`, `Lists.SelectedOperations.Selected` | Entra-Consent + Freigabe pro Element |

## Fehlerbehebung

| Symptom | Ursache / Lösung |
| --- | --- |
| `AADSTS700027` / Zertifikatsfehler | Hochgeladenes Zertifikat passt nicht zum Schlüssel in der PEM, oder es ist abgelaufen. Neu erzeugen und erneut hochladen. |
| `AADSTS90002: Tenant not found` | Falsche `tenant_id`. |
| Token funktioniert, Postfach-Aufrufe liefern 403 | RBAC-Scope noch nicht aktiv (bis zu 2 h warten), oder das Postfach liegt außerhalb des Management Scope. Mit `Test-ServicePrincipalAuthorization` prüfen. |
| App kann Postfächer außerhalb des Scopes lesen | Eine tenant-weite Entra-Berechtigung ist noch vorhanden — entfernen (siehe Warnung in §3). |
| `drive`/`sp`-Aufrufe liefern 403 | Für diese Site / dieses OneDrive existiert noch keine `Sites.Selected`-Freigabe — gemäß §4 gewähren. |

## Referenzen

- [RBAC for Applications in Exchange Online](https://learn.microsoft.com/en-us/exchange/permissions-exo/application-rbac)
- [Sites.Selected / per-site permissions](https://learn.microsoft.com/en-us/graph/api/site-post-permissions)
- [Overview of Selected permissions in OneDrive and SharePoint](https://learn.microsoft.com/en-us/graph/permissions-selected-overview)
- [Graph app-only auth (client credentials)](https://learn.microsoft.com/en-us/graph/auth-v2-service)
