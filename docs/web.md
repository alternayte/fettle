# Web search and web pages

Bots have two web tools that run in the Fettle server, not in a computer:

- `web_search(query, count)` returns titles, URLs and snippets. `count` is 1 to 20. The default is 5.
- `web_fetch(url)` returns the readable text of one page.

A bot with no computer can use both tools.
A bot uses the browser in its computer for pages that need a sign-in, clicks or forms.

Both tools have the class `read`.
Their results have the origin `web`, so the bot treats the text as data.
Both tools use the server HTTP client, with its proxy and certificate settings.

## Search provider

Set the search provider in **Settings**, **Web**.
A space has one search provider.
When a space has no search provider, `web_search` is off, and bots use the browser to search.
`web_fetch` needs no provider.

| Provider | What you give | Cost of one search |
| --- | --- | --- |
| Tavily | API key | $0.008 |
| Exa | API key | The cost that Exa sends in each response |
| SearXNG | The URL of your SearXNG server, and an optional key | $0 |
| Brave Search | API key | $0.005 |

The prices come from the price pages of the providers on 2026-11-17.
Fettle uses them when the response does not give a cost.

Fettle keeps the key in the vault of the space as a secret item.
The API never returns the key. The page shows only its last four characters.

**Test** on the page runs one search and shows the results.
A test search counts as one search in the cost of the space.

### SearXNG

SearXNG must allow the JSON format.
Add `json` to `search.formats` in the `settings.yml` of SearXNG.
If SearXNG answers HTTP 403, the JSON format is off.
Fettle sends the key as a bearer token, when you give one.

### Brave Search

The run journal keeps the search results of each run.
The holder of the key accepts the [Brave Search API terms](https://api-dashboard.search.brave.com/terms-of-service).
Read the terms before you use Brave Search.

## Reading pages

`web_fetch` reads at most 10 MB of a page.
It returns at most 64 KB of text. A line at the end shows when it cut the text.

| Content | Result |
| --- | --- |
| HTML | The main article, as Markdown |
| PDF | The text of each page |
| Plain text, JSON, XML | The text as it is |

`web_fetch` cannot read a PDF that has no text layer. Use the browser for it.

### Private addresses

`web_fetch` does not read private or local addresses.
It refuses these addresses after it resolves the host name:

- Loopback, such as `127.0.0.1` and `::1`.
- Private networks, such as `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` and `fc00::/7`.
- Link-local addresses, with the cloud metadata address `169.254.169.254`.
- Carrier-grade NAT, `100.64.0.0/10`.
- Unspecified, multicast and reserved addresses.

When the server uses a proxy, Fettle resolves the host before it sends the request to the proxy, and checks the addresses.
A proxy on a private network is not refused.
When this host cannot resolve a name, Fettle sends the request to the proxy, which resolves it. Many work networks resolve public names only in the proxy.
A name that resolves on this host to a private address is still refused, and the allowlist of the zone still applies.

`web_fetch` follows at most 5 redirects.
It checks the host and the addresses of each redirect.
An error names the host that Fettle refused.

### Zones

`web_fetch` follows the network policy of the zone of the bot:

- In an open zone, it reads any public host.
- In an allowlist zone, it reads only the hosts on the list. `*.example.com` allows `example.com` and its subdomains. An entry can be an IP address.
- A bot with no zone gets the open policy.

Search results can list any URL, also in an allowlist zone.
`web_fetch` still refuses a result that is off the list.

## Cost

Each search writes one row in the cost of the space.
The row has the kind `search`, the unit `calls`, and the provider name in the model column.
A search in a run adds its cost to the run.
Search cost counts toward the budgets of the envelope, the bot and the space.
**Settings**, **Cost** shows the searches in their own column.

## Prompt

When a run has both tools, the bot gets this rule:
"Use web_search and web_fetch for facts; use the browser for sign-in, clicks and forms."
When a run has only `web_fetch`, the rule names only `web_fetch`.
