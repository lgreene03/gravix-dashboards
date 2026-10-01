<!-- Copyright 2026 The Gravix Authors -->
<!-- SPDX-License-Identifier: BUSL-1.1 -->

# `ee/tenancy/byob` — bring your own bucket

A Gravix Cloud tenant can keep every fact Gravix stores in an S3-compatible bucket the tenant owns,
instead of in the shared Cloud bucket. This package registers that bucket, proves Gravix can use it,
and hands the unmodified open-source ingestion and rollup binaries the settings that point them at
it.

**If you self-host, you do not need this.** Your facts already sit in storage you own. Set
`S3_BUCKET` and the related variables in your own deployment, and that is the whole feature. This
package exists only because Gravix Cloud runs installations on other people's behalf.

## What your bucket must allow

Registration writes a 32-byte random object under `gravix-byob-verify/`, reads it back, compares the
bytes, deletes it, and checks that it is gone. Each step catches a different misconfiguration, so
the credentials you register need all four permissions on the bucket:

| Operation | S3 action | Why Gravix needs it |
|---|---|---|
| Write | `s3:PutObject` | Ingestion writes facts |
| Read | `s3:GetObject` | Rollups read facts back |
| Check | `s3:GetObject` (HEAD) | Rollups and retention check what exists |
| Delete | `s3:DeleteObject` | Retention purges data older than your plan's window |

**Object lock and versioning that keep deleted objects readable will fail registration.** Such a
bucket would accept every fact and then refuse every retention purge, which is a compliance problem
found months later. Registration finds it on the first call instead.

## Registering a bucket

Every call needs a Gravix Cloud token for an **admin of the tenant being named**, the same token
`POST /api/gateway/login` returns. An admin of one tenant cannot register or read another's bucket.

```bash
curl -X POST https://<cloud-host>/byob/buckets \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"tenant_id":"t-123","endpoint":"https://s3.us-east-1.amazonaws.com","region":"us-east-1",
       "bucket":"acme-gravix","access_key_id":"AKIA...","secret_access_key":"..."}'
```

| Response | Meaning |
|---|---|
| `201` `{"status":"verified"}` | The bucket passed all five checks and is in use |
| `422` `{"status":"failed","error":"..."}` | A check failed; the error names which one. The registration is stored as failed so you can fix the bucket and re-verify |
| `400` | A field is missing. All six are required |
| `401` / `403` | No valid token, or not an admin of this tenant |
| `402` | Your Pro licence has lapsed. Existing configuration still works and can be exported; it cannot be changed |

Re-run the five checks at any time, for example after changing a bucket policy:

```bash
curl -X POST https://<cloud-host>/byob/buckets/t-123/verify -H "Authorization: Bearer $TOKEN"
```

Read the current registration:

```bash
curl https://<cloud-host>/byob/buckets/t-123 -H "Authorization: Bearer $TOKEN"
```

## Where your secret goes

- **It is never returned.** The read response has no secret field at all, and the access key id is
  shown with all but its first and last four characters masked.
- **It is stored apart from everything else.** Registrations live in their own SQLite database,
  `byob.db`, with its own migrations. Core's tenant database, which more of Gravix reads, never holds
  it.
- **It reaches exactly two kinds of process.** Your ingestion and rollup deployments receive five
  environment variables, which the open-source binaries already read:

  ```
  S3_ENDPOINT  S3_REGION  S3_BUCKET  S3_ACCESS_KEY  S3_SECRET_KEY
  ```

  Nothing in the core knows this package exists. Bring-your-own-bucket is a deployment setting for
  software that already supports it, not a separate data path.

## Leaving

Your facts are in your bucket as JSONL and Parquet, in the layout `docs-site/docs/bare-parquet-access.md`
describes, readable with no Gravix component running.
