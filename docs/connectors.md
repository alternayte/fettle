# Connectors

A connector lets bots use another app through MCP, the Model Context Protocol.
A connector has accounts. A zone gets an account of a connector as a grant.
Bots in the zone can then call the tools of the connector through that account.

Add connectors in **Settings**, **Connectors**.

## Kinds

| Kind | Connection |
| --- | --- |
| Remote | An MCP server at a Streamable HTTP URL. |
| In the computer | A stdio MCP server. Its command runs in the zone computer of the bot. |
| Composio | The Composio MCP endpoint, with a Composio key. |

A remote connector signs in in one of three ways:

- **OAuth sign-in.** Fettle uses OAuth 2.1 with PKCE. Fettle finds the authorization server of the MCP URL and registers itself as a client.
- **Key in a header.** You give the header name and, for each account, the key.
- **None.** The server needs no sign-in.

The OAuth redirect URI is `<FETTLE_BASE_URL>/api/v1/oauth/callback` on a server.
Fettle keeps the tokens and the OAuth client secret sealed with the data key of the space. It refreshes an OAuth token when it expires.

The instance setting `connectors.composio_url` sets the Composio endpoint. The default is `https://mcp.composio.dev/mcp`.

## Catalogue

The Connect screen lists known MCP servers. It also accepts any URL or command.

| Name | URL or command | Sign-in |
| --- | --- | --- |
| Notion | `https://mcp.notion.com/mcp` | OAuth |
| Linear | `https://mcp.linear.app/mcp` | OAuth |
| Sentry | `https://mcp.sentry.dev/mcp` | OAuth |
| GitHub | `https://api.githubcopilot.com/mcp/` | key in the `Authorization` header |
| Stripe | `https://mcp.stripe.com` | key in the `Authorization` header |
| Cloudflare docs | `https://docs.mcp.cloudflare.com/mcp` | none |
| Atlassian Cloud | `uvx mcp-atlassian@0.24.6` | variables |
| Atlassian Server/Data Center | `uvx mcp-atlassian@0.24.6` | variables |

A stdio entry names its variables. The Connect screen asks for them.

| Entry | Plain variables | Secret variables |
| --- | --- | --- |
| Atlassian Cloud | `JIRA_URL`, `CONFLUENCE_URL` | `JIRA_USERNAME`, `JIRA_API_TOKEN`, `CONFLUENCE_USERNAME`, `CONFLUENCE_API_TOKEN` |
| Atlassian Server/Data Center | `JIRA_URL`, `CONFLUENCE_URL` | `JIRA_PERSONAL_TOKEN`, `CONFLUENCE_PERSONAL_TOKEN` |

The Jira variables are required. The Confluence variables are optional.
The user name of Atlassian Cloud is a secret variable, because each account is a different user.
The catalogue pins the version of `mcp-atlassian`, so each start runs the same server.

## Variables

A stdio MCP server reads its settings from environment variables. Fettle splits them in two:

- **Plain variables** belong to the connector. Examples: `JIRA_URL`, `CONFLUENCE_URL`. You see and edit them in the connector.
- **Secret variables** belong to an account. Examples: `JIRA_PERSONAL_TOKEN`, `JIRA_API_TOKEN`.

Fettle seals each secret variable with the data key of the space, as it seals a header key.
A secret value is write only. After you save it, Fettle shows only its name and "set".
The API returns the names in `secret_env` of the account, never the values.

Each account starts its own process of the server.
The process gets the plain variables of the connector and the secret variables of the account.
When a name is in both, the value of the account wins.
Two accounts with different tokens thus act as two different users.

Fettle sends the command and the variables to the computer in the first message of the connection. They are not in a URL.
The secret values are not in the command line, so `ps` in the computer does not show them.
Fettle removes the secret values from logs, the journal and tool output.

If a required variable has no value, **Test** fails with a message that names the variable.

Remote connectors have no variables. A remote connector uses a header key.

API fields:

- `env` on create and edit of a connector: the plain variables, as name and value.
- `secret_env` on create and edit of an account: the secret variables, as name and value. On an edit, an empty value removes the variable.

## Edit

Select **Edit** on a connector to change it. You can change:

- the name,
- the URL of a remote connector,
- the command and the plain variables of a stdio connector,
- the header name of a connector with a header key.

The kind and the sign-in cannot change. To change them, add a new connector.

Select **Edit** on an account to change it. You can change:

- the name of the account,
- the key of an account with a header key,
- the secret variables of a stdio account.

The edit form does not show the old key or the old secret values. Type a new value to replace one.

An edit keeps the accounts, the grants, the tool classes and the tools that are turned off.
Each edit writes an audit row: `connector.update` for a connector and `connector.account_update` for an account.
The audit row names the changed fields. It does not hold a value of a key or a secret variable.

After an edit, Fettle lists the tools again at the next **Test** or run.
A tool that the server no longer lists loses its class and its on or off setting.
A new tool gets its class from the rules in [Tool classes](#tool-classes).

A new name changes the tool names from the next run. For example, `mail__send_email` becomes `teammail__send_email`.
In the same transaction, Fettle changes the policy rules that name the connector or its tools.

A new URL of an OAuth connector needs a new sign-in:

1. Fettle warns you before it saves the change.
2. Fettle registers itself again at the sign-in server of the new URL.
3. Fettle deletes the tokens of each account. The accounts and their grants stay.
4. Each account shows **Sign in again**. Select it and sign in.

The API is `PATCH /api/v1/spaces/{space}/connectors/{connector}` for a connector, and `PATCH /api/v1/spaces/{space}/connectors/{connector}/accounts/{account}` for an account.

## Steps

1. Add the connector.
2. Add an account. For OAuth, sign in at the URL that Fettle opens.
3. Select **Test** to list the tools with their classes.
4. Select **Give to a zone**. A card in the chat of your primary bot asks you to allow the grant.
5. Allow the card. Bots in the zone can now use the account.

## Tool classes

Each tool has a class. The class decides when a bot must ask first.

| Class | Meaning |
| --- | --- |
| `read` | Reads data. |
| `write_local` | Changes data in the computer. |
| `write_external` | Changes data outside the computer. |
| `send` | Sends a message to a person. |
| `admin` | Changes bots, routines or settings. |

Fettle sets the class of a connector tool from its MCP annotations and its text:

1. A tool with `readOnlyHint` is `read`.
2. A tool with `destructiveHint` is `write_external`.
3. A tool whose name or description says send, post, reply or message is `send`.
4. All other tools are `write_external`.

An owner can change the class of a tool, or turn the tool off. The change applies to the next run.
Read [security.md](security.md#approvals) for how classes and approvals work together.

## Delete

Deleting a connector also deletes its accounts and their grants.
