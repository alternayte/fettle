# Enterprise

The `ee` directory holds the Enterprise features.
Only a build with the Go build tag `ee` includes them.
Core packages never import `ee`. The default build links no `ee` code.

```sh
go build -tags ee -o bin/fettle ./cmd/fettle
```

The files in `ee/` are under the Fettle Enterprise License in [ee/LICENSE](../ee/LICENSE).
Production use needs a valid licence key. Development, tests, evaluation and demonstration need no key.

## Features

| Feature | Licence name |
| --- | --- |
| Single sign-on with OIDC and SAML | `sso` |
| SCIM 2.0 provisioning | `scim` |
| Audit log export to S3-compatible storage and a signed webhook | `audit_export` |
| Organization policy packs | `policy_packs` |
| Organization budgets and quotas per person | `org_budgets` |
| Admin console | `admin_console` |

A licence names its features. The name `all` gives all features.

All Enterprise routes are for instance admins.

## Licence key

Fettle verifies the licence key offline. Fettle does not contact a licence server.

- A key has the form `<body>.<signature>`, both base64url.
- The body is JSON. It holds the licence ID, the customer, the seats, the features, the issue date and the expiry date.
- The signature is Ed25519. The public key of the issuer is in the binary.
- After the expiry date, the key works for 14 more days.
- After these 14 days, the Enterprise settings are read-only.

Set the key in one of two ways:

- Set `FETTLE_LICENSE` in the environment of the server. It overrides the stored key.
- Send `PUT /api/v1/admin/license` with `{"key": "<key>"}`.

`GET /api/v1/admin/edition` shows the edition, the licence state and each feature.
The licence states are `missing`, `invalid`, `valid`, `grace` and `read_only`.

## Single sign-on

Server mode signs people in with OpenID Connect, with SAML 2.0, or with both.
The licence must have the `sso` feature. The sign-in page shows one button for each method.
Set the variables in the environment of the server, then restart it.

### OpenID Connect

| Variable | Use |
| --- | --- |
| `FETTLE_OIDC_ISSUER` | The issuer URL. Fettle reads `<issuer>/.well-known/openid-configuration`. |
| `FETTLE_OIDC_CLIENT_ID` | The client ID that the identity provider gave to Fettle. |
| `FETTLE_OIDC_CLIENT_SECRET` | The client secret. |
| `FETTLE_OIDC_GROUPS_CLAIM` | The ID token claim that holds the groups. The default is `groups`. Fettle also asks for a scope with this name. |
| `FETTLE_OIDC_NAME` | The name on the sign-in button, for example `Acme ID`. |

Register this redirect URI at the identity provider: `<FETTLE_BASE_URL>/api/auth/oauth/sso/callback`.

Fettle checks the signature, the issuer, the audience, the expiry and the nonce of each ID token.
A new person gets an account at the first sign-in. A person with a verified email that has an account gets that account.

### SAML 2.0

| Variable | Use |
| --- | --- |
| `FETTLE_SAML_IDP_METADATA` | The URL or the file path of the identity provider metadata. |
| `FETTLE_SAML_GROUPS_ATTRIBUTE` | The attribute that holds the groups. The default is `groups`. |
| `FETTLE_SAML_NAME` | The name on the sign-in button. |

Give these values to the identity provider:

- Entity ID and metadata: `<FETTLE_BASE_URL>/api/v1/sso/saml/metadata`
- Assertion consumer service (HTTP-POST): `<FETTLE_BASE_URL>/api/v1/sso/saml/acs`
- Name ID format: email address

The identity provider must sign the response or the assertion. Fettle does not accept encrypted assertions.
Fettle accepts only answers to a sign-in that it started. A sign-in that starts at the identity provider does not work.
Fettle reads the email from the name ID or from the `email` or `mail` attribute.

### Groups

At each single sign-on, Fettle stores the groups of the person.
The group roles below apply to these groups and to the SCIM groups.
A person who leaves all mapped groups of a space leaves the space at the next sign-in.

## SCIM 2.0

Fettle serves SCIM 2.0 under `/scim/v2`:

- `/scim/v2/Users`: list, create, read, replace, patch and delete users.
- `/scim/v2/Groups`: list, create, read, replace, patch and delete groups.

SCIM requests use a bearer token.
`POST /api/v1/admin/scim/token` makes a new token. The old token stops working. Fettle keeps only the SHA-256 of the token.

- A new SCIM user gets an account without a password.
- A delete disables the account and ends its sessions.

### Group roles

`PUT /api/v1/admin/group-roles` maps groups to roles in spaces:

```json
{
  "mappings": [
    { "group": "acme-research", "space": "<space-id>", "role": "member" }
  ]
}
```

Fettle adds the members of a group to the space with the role. The groups come from SCIM and from single sign-on.

## Audit log export

Fettle sends the audit rows of all spaces to an S3-compatible bucket, to a webhook, or to both.
It sends the rows in batches of 1000.
A failed delivery keeps the cursor. The next run sends the same batch again under the same file name.

| Variable | Effect |
| --- | --- |
| `FETTLE_AUDIT_S3_BUCKET` | The bucket. The S3 export is on when it is set. |
| `FETTLE_AUDIT_S3_ENDPOINT` | The endpoint of the S3-compatible store. |
| `FETTLE_AUDIT_S3_REGION` | The region. |
| `FETTLE_AUDIT_S3_ACCESS_KEY` | The access key. |
| `FETTLE_AUDIT_S3_SECRET_KEY` | The secret key. |
| `FETTLE_AUDIT_S3_PATH_STYLE` | `true` uses path-style URLs. |
| `FETTLE_AUDIT_S3_PREFIX` | The key prefix of the files. The default is `audit/`. |
| `FETTLE_AUDIT_WEBHOOK_URL` | The webhook URL. |
| `FETTLE_AUDIT_WEBHOOK_SECRET` | The secret that signs each delivery. |

Each file is NDJSON, with one audit row per line.
Its key is `<prefix><yyyy>/<mm>/<dd>/<time>-<id>.ndjson`.
The instance setting `ee.audit_retention_days` sets how long files stay in the bucket. The default is 365 days.

A webhook delivery is a `POST` with the body `application/x-ndjson`. It has two headers:

- `X-Fettle-Timestamp`: the time of the delivery.
- `X-Fettle-Signature`: the hex HMAC-SHA256 of `<timestamp>.<body>` with the webhook secret.

Check the signature before you use the body.

- `GET /api/v1/admin/audit-export` shows the targets, the cursor and the retention.
- `POST /api/v1/admin/audit-export/run` runs the export now.

## Policy packs

A policy pack applies to every space.

```json
{
  "rules": [
    { "tool": "send_email", "recipient": "*@acme.test", "effect": "ask" },
    { "site": "*.untrusted.acme.test", "effect": "block" }
  ],
  "zone_hosts": ["*.acme.test"],
  "providers": ["docker", "applecontainer"],
  "models": ["anthropic/*", "gpt-5*"]
}
```

| Field | Effect |
| --- | --- |
| `rules` | Approval rules for every space. Each rule has `tool`, `connector`, `site` or `recipient`, and an `effect`. |
| `zone_hosts` | Every zone must use an allowlist inside these host patterns. |
| `providers` | The provider kinds that zones can use. |
| `models` | The model name patterns that roles can use. |

A block rule of the pack wins over every rule of a space.

- `GET /api/v1/admin/policy-pack` returns the pack.
- `PUT /api/v1/admin/policy-pack` sets the pack.

## Organization budgets

```sh
curl -X PUT -H "Authorization: Bearer $FETTLE_TOKEN" -H "Content-Type: application/json" \
  -d '{"org_month_usd": 500, "user_month_usd": 50}' \
  https://fettle.acme.test/api/v1/admin/org-budgets
```

- `org_month_usd` is the budget of all spaces per calendar month in USD.
- `user_month_usd` is the quota per person per calendar month in USD. It counts the work that the person started.
- Zero has no limit.

Before each model call, Fettle checks the budgets.
When a call can go over a budget, the run stops and posts a **Budget reached** card.

## Admin console

The admin console is a set of API routes for instance admins:

- `GET /api/v1/admin/usage-report?days=30` returns the usage of every space: members, bots, runs, tokens, computer time and cost. It also returns the seats in use.
- `GET /api/v1/admin/edition` returns the licence and the features.
- The policy pack, the budgets, the group roles and the audit export have their routes above.
