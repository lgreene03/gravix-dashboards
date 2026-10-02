// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// F-076 regression guard. Run with `make test-js`.
//
// Cube takes a query's security context from req.securityContext, or from a
// `security_context` that checkAuth returns. Both v0.35 and v1.7.48 do this in
// api-gateway's wrapCheckAuth, and both ignore any other return value.
// cube/cube.js returned `{ securityContext: … }`, so Cube saw no security
// context, queryRewrite added no tenant filter, and every token read every
// tenant's rows. These tests hold the contract: checkAuth must leave the tenant
// on req.securityContext, and queryRewrite must filter the cube the query reads.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import vm from 'node:vm';

const here = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(join(here, '..', '..', 'cube', 'cube.js'), 'utf8');

// loadConfig evaluates cube/cube.js with a stand-in for jsonwebtoken, which is
// in Cube's image and not in this repository: a token "good:<tenant>" verifies
// to that tenant, and anything else is rejected.
function loadConfig(env) {
    const jwt = {
        verify(token, secret) {
            assert.equal(secret, env.JWT_SECRET, 'checkAuth verified with a different secret');
            if (!token.startsWith('good:')) throw new Error('invalid signature');
            return { tenant_id: token.slice('good:'.length) || undefined };
        },
    };
    const module = { exports: {} };
    vm.runInNewContext(source, {
        module,
        exports: module.exports,
        process: { env },
        require: (name) => {
            if (name === 'jsonwebtoken') return jwt;
            if (name === 'fs') return { readFileSync: () => { throw new Error('no secret file in this test'); } };
            throw new Error(`unexpected require: ${name}`);
        },
    });
    return module.exports;
}

// cubeWrapCheckAuth is what Cube does with checkAuth's result (api-gateway
// wrapCheckAuth, the same in v0.35 and v1.7.48), so the test asserts on the
// security context Cube will actually use, not on what checkAuth returns.
async function securityContextFor(config, auth) {
    const req = {};
    const result = await config.checkAuth(req, auth);
    if (result && result.security_context) req.securityContext = result.security_context;
    return plain(req.securityContext);
}

// Objects made inside the vm context have that context's prototypes, which
// strict deep equality compares; JSON makes them plain values.
const plain = (v) => (v === undefined ? v : JSON.parse(JSON.stringify(v)));

const env = { JWT_SECRET: 'test-secret-at-least-32-characters-long' };

test('checkAuth hands Cube the tenant from the token', async () => {
    const ctx = await securityContextFor(loadConfig(env), 'Bearer good:tenant-a');
    assert.deepEqual(ctx, { tenant_id: 'tenant-a' });
});

test('checkAuth refuses a token that does not verify', async () => {
    await assert.rejects(securityContextFor(loadConfig(env), 'Bearer forged'), /Invalid or expired token/);
});

test('every query from a tenant is filtered to that tenant, on the cube it reads', () => {
    const config = loadConfig(env);
    const ctx = { securityContext: { tenant_id: 'tenant-a' } };
    const cases = [
        [{ measures: ['RequestMetricsMinute.requestCount'] }, 'RequestMetricsMinute.tenantId'],
        // The dashboard's events log asks for ServiceEvents dimensions only. A
        // filter on another cube would fail it: no cube joins another.
        [{ dimensions: ['ServiceEvents.eventTime', 'ServiceEvents.message'] }, 'ServiceEvents.tenantId'],
        [{ dimensions: ['ServiceEventsDaily.service'] }, 'ServiceEventsDaily.tenantId'],
        [{ timeDimensions: [{ dimension: 'ServiceEvents.eventTime', granularity: 'hour' }] }, 'ServiceEvents.tenantId'],
    ];
    for (const [query, member] of cases) {
        const out = plain(config.queryRewrite(structuredClone(query), ctx));
        assert.deepEqual(out.filters, [{ member, operator: 'equals', values: ['tenant-a'] }],
            `query ${JSON.stringify(query)} was not filtered to its tenant`);
    }
});

test('a tenant filter is added to the filters a query already has', () => {
    const out = plain(loadConfig(env).queryRewrite(
        { measures: ['RequestMetricsMinute.requestCount'],
          filters: [{ member: 'RequestMetricsMinute.service', operator: 'equals', values: ['checkout'] }] },
        { securityContext: { tenant_id: 'tenant-b' } }));
    assert.equal(out.filters.length, 2);
    assert.deepEqual(out.filters[1], { member: 'RequestMetricsMinute.tenantId', operator: 'equals', values: ['tenant-b'] });
});
