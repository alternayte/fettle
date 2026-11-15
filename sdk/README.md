# Fettle SDK

This folder holds the generated clients and schemas of the Fettle HTTP API. The Apache License 2.0 applies to this folder (see LICENSE). The rest of Fettle uses the AGPL-3.0.

## Contents

- `openapi.json`: the OpenAPI 3.1 document of the API.
- `schemas/envelope.schema.json`: the JSON Schema of an envelope, one unit of work.
- `schemas/run-event.schema.json`: the JSON Schema of one run event of a journal.
- `typescript/`: a typed TypeScript client.

`just gen` writes all of these files from the server code. Do not edit them by hand.

## TypeScript client

```ts
import { createFettleClient } from "fettle-sdk";

const fettle = createFettleClient({ baseUrl: "https://fettle.acme.test", token: process.env.FETTLE_TOKEN ?? "" });
const { data } = await fettle.GET("/api/v1/me");
```

Make a personal access token in Settings, Tokens. A local Fettle uses the local token in the data folder.
