// Cloudflare Worker：new-api 多节点健康路由（自包含版）
// - 节点表：有 NODES_KV 绑定时以 KV "nodes" 为准（运行时可动态调整），否则用内置 DEFAULT_NODES
//   → 即使 Cloudflare token 没有 KV 权限，本 Worker 也能直接上线工作（内置两节点兜底）
// - 流量：按 weight 加权随机选健康节点，5xx 自动顺延下一家，全挂 502
// - /__router/nodes：返回当前生效节点表（验收用）
// - /__internal/heartbeat：X-Router-Key 鉴权上报健康（有 KV 才落库，无 KV 时无状态）
// - 响应带 X-Router-Node 头，便于压测确认分流命中
const DEFAULT_NODES = [
  { id: 'node1', url: 'https://newapi-public-production.up.railway.app', provider: 'railway438', weight: 3, enabled: true, healthy: true },
  { id: 'node2', url: 'https://newapi-public-production-49b4dfb0.up.railway.app', provider: 'railway842', weight: 2, enabled: true, healthy: true },
  { id: 'node4', url: 'https://newapi-public-production-91f2142c.up.railway.app', provider: 'railway196', weight: 2, enabled: true, healthy: true },
  { id: 'node5', url: 'https://newapi-public-production-1c18a29a.up.railway.app', provider: 'railway850', weight: 2, enabled: true, healthy: true },
  { id: 'node6', url: 'https://newapi-public-production-782d8c37.up.railway.app', provider: 'railway733', weight: 2, enabled: true, healthy: true },
  { id: 'node7', url: 'https://newapi-public-production-2113ed4c.up.railway.app', provider: 'railway964', weight: 2, enabled: true, healthy: true },
];

async function getNodes(env) {
  if (env.NODES_KV) {
    try {
      const raw = await env.NODES_KV.get('nodes');
      if (raw) {
        const parsed = JSON.parse(raw);
        const nodes = Array.isArray(parsed) ? parsed : (parsed && parsed.nodes);
        if (Array.isArray(nodes) && nodes.length) return nodes;
      }
    } catch (e) { /* KV 读失败 → 回落到内置节点表 */ }
  }
  return DEFAULT_NODES;
}

function weightedOrder(nodes) {
  const arr = [];
  for (const n of nodes) {
    const w = Math.max(1, n.weight || 1);
    for (let i = 0; i < w; i++) arr.push(n);
  }
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
  return [...new Set(arr)];
}

export default {
  async fetch(req, env) {
    const url = new URL(req.url);

    if (url.pathname === '/__router/nodes') {
      return Response.json({ ok: true, source: env.NODES_KV ? 'kv' : 'builtin', nodes: await getNodes(env) });
    }

    if (url.pathname === '/__internal/heartbeat' && req.method === 'POST') {
      const secret = env.ROUTER_SHARED_SECRET || globalThis.ROUTER_SHARED_SECRET || '';
      if (secret && req.headers.get('X-Router-Key') !== secret) return new Response('forbidden', { status: 403 });
      const report = await req.json().catch(() => ({}));
      if (env.NODES_KV) {
        const nodes = await getNodes(env);
        const n = nodes.find((x) => x.id === report.id);
        if (n) {
          n.healthy = !!report.healthy;
          n.latencyMs = report.latencyMs;
          n.lastBeat = Date.now();
          await env.NODES_KV.put('nodes', JSON.stringify(nodes));
        }
      }
      return Response.json({ ok: true, stateless: !env.NODES_KV });
    }

    const nodes = (await getNodes(env)).filter((n) => n.enabled && n.healthy !== false);
    if (!nodes.length) return new Response('no healthy node', { status: 503 });

    let lastErr = null;
    for (const node of weightedOrder(nodes).slice(0, 3)) {
      try {
        const target = new URL(req.url);
        const u = new URL(node.url);
        target.protocol = u.protocol;
        target.host = u.host;
        const resp = await fetch(new Request(target.toString(), req), { cf: { cacheEverything: false } });
        if (resp.status < 502) {
          const h = new Headers(resp.headers);
          h.set('X-Router-Node', node.id);
          return new Response(resp.body, { status: resp.status, statusText: resp.statusText, headers: h });
        }
        lastErr = new Error('upstream ' + resp.status);
      } catch (e) { lastErr = e; }
    }
    return new Response('all nodes failed: ' + (lastErr && lastErr.message), { status: 502 });
  },
};

