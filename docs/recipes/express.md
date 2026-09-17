# Express

Gravix's Node SDK ships an Express middleware, so instrumenting an existing app is two lines:
construct a client and `app.use` it.

The middleware records the **raw** request path (`req.path`), and `client.recordFact` sanitizes it
before sending — a segment of four or more digits becomes `{id}`. That threshold matters: a
two-digit id like `/users/42` is *not* rewritten client-side, so a route whose ids are always short
needs the translation done explicitly, the way the Flask and Django recipes do it.

**The SDK is ESM-only.** Its `package.json` declares `"type": "module"` and exports only an `import`
condition, so `require('@gravix/sdk')` fails with `ERR_PACKAGE_PATH_NOT_EXPORTED`. This example sets
`"type": "module"` and uses `import`. In a CommonJS app, reach it with a dynamic
`const { GravixClient } = await import('@gravix/sdk');`.

## The code

`examples/recipes/express/server.js` — this block and that file are byte-identical, and a test fails if they drift.

```javascript
// Gravix recipe: Express
//
// The Gravix Node SDK is ESM-only, so this file uses `import`, not `require`,
// and package.json sets "type": "module". A CommonJS Express app cannot
// `require('@gravix/sdk')` — use a dynamic `await import(...)` there.
//
// Run:
//   npm install
//   GRAVIX_ENDPOINT=http://localhost:8090 GRAVIX_API_KEY=$(cat ../../../data/api_key.txt) node server.js
//   curl http://localhost:3000/users/1234

import express from 'express';
import { GravixClient, expressMiddleware } from '@gravix/sdk';

const client = new GravixClient({
  baseUrl: process.env.GRAVIX_ENDPOINT || 'http://localhost:8090',
  apiKey: process.env.GRAVIX_API_KEY || '',
  service: 'my-express-app',
  // One fact per request, so anything you send shows up immediately.
  // Raise this in production to reduce request volume.
  batchSize: 1,
});

const app = express();

// Records every request. The SDK replaces id-shaped path segments with {id}
// before sending, so /users/1234 is reported as /users/{id} and your
// dimension cardinality stays bounded.
app.use(expressMiddleware(client));

app.get('/users/:id', (req, res) => {
  res.json({ id: req.params.id });
});

const port = Number(process.env.PORT || 3000);
app.listen(port, () => {
  console.log(`listening on ${port}`);
});
```

## Running it

```bash
cd examples/recipes/express
npm install
GRAVIX_ENDPOINT=http://localhost:8090 GRAVIX_API_KEY=$(cat ../../../data/api_key.txt) node server.js
curl http://localhost:3000/users/1234
```

The fact Gravix receives has `path_template: "/users/{id}"`, not `/users/1234`.

## See also

- [All recipes](README.md)
- [Getting started](../../README.md)
