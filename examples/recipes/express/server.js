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
