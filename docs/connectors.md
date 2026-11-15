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
Fettle keeps the tokens sealed with the data key of the space. It refreshes an OAuth token when it expires.

The instance setting `connectors.composio_url` sets the Composio endpoint. The default is `https://mcp.composio.dev/mcp`.

## Catalogue

The Connect screen lists known remote MCP servers. It also accepts any URL.

| Name | URL | Sign-in |
| --- | --- | --- |
| Notion | `https://mcp.notion.com/mcp` | OAuth |
| Linear | `https://mcp.linear.app/mcp` | OAuth |
| Sentry | `https://mcp.sentry.dev/mcp` | OAuth |
| GitHub | `https://api.githubcopilot.com/mcp/` | key in the `Authorization` header |
| Stripe | `https://mcp.stripe.com` | key in the `Authorization` header |
| Cloudflare docs | `https://docs.mcp.cloudflare.com/mcp` | none |

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
