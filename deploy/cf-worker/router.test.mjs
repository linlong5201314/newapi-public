import assert from 'node:assert/strict';
import { test } from 'node:test';
import worker from './router.js';

const secret = 'test-only-router-shared-secret';
const env = { ROUTER_SHARED_SECRET: secret };

test('heartbeat and node table always require authentication', async () => {
  for (const [method, path] of [['GET', '/__router/nodes'], ['POST', '/__internal/heartbeat']]) {
    const response = await worker.fetch(new Request('https://router.example' + path, { method }), {});
    assert.equal(response.status, 403);
  }
  const response = await worker.fetch(new Request('https://router.example/__router/nodes', { headers: { 'X-Router-Key': secret } }), env);
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.equal(body.nodes.length, 7);
  assert.ok(body.nodes.some((node) => node.id === 'node8' && node.enabled && node.healthy));
});

test('stale KV heartbeat does not route traffic', async () => {
  const nodes = [{ id: 'stale', url: 'https://stale.example', enabled: true, healthy: true, lastBeat: Date.now() - 120000 }];
  const response = await worker.fetch(new Request('https://router.example/api/status'), {
    ROUTER_SHARED_SECRET: secret,
    NODES_KV: { async get() { return JSON.stringify({ nodes }); } },
  });
  assert.equal(response.status, 503);
});

test('every enabled node is tried and internal errors are not exposed', async () => {
  const calls = [];
  globalThis.fetch = async (url) => {
    calls.push(String(url));
    return new Response('upstream unavailable', { status: 502 });
  };
  const response = await worker.fetch(new Request('https://router.example/api/status'), env);
  assert.equal(response.status, 502);
  assert.equal(calls.length, 7);
  assert.equal(await response.text(), 'All upstream nodes are temporarily unavailable');
});

test('KV node schema preserves enabled, weight and health fields', async () => {
  const nodes = [{ id: 8, url: 'https://node8.example', enabled: true, weight: 3, healthy: true, lastBeat: Date.now() }];
  const response = await worker.fetch(new Request('https://router.example/__router/nodes', { headers: { 'X-Router-Key': secret } }), {
    ROUTER_SHARED_SECRET: secret,
    NODES_KV: { async get() { return JSON.stringify({ nodes }); } },
  });
  assert.equal(response.status, 200);
  const body = await response.json();
  assert.deepEqual(body.nodes, [{ id: '8', url: 'https://node8.example', enabled: true, weight: 3, healthy: true, lastBeat: nodes[0].lastBeat }]);
});
