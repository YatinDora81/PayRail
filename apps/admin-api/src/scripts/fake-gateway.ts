const seen: Record<string, unknown>[] = [];
Bun.serve({
  port: Number(process.env.FAKE_GATEWAY_PORT ?? 8083),
  async fetch(req) {
    const url = new URL(req.url);
    if (req.method === 'POST' && url.pathname === '/v1/refunds') {
      const body = (await req.json()) as { amountMinor?: unknown };
      const key = req.headers.get('idempotency-key');
      seen.push({ key, body, traceparent: req.headers.get('traceparent') });
      if (!key || !/^\d+$/.test(String(body.amountMinor))) return Response.json({ error: 'bad' }, { status: 400 });
      if (body.amountMinor === '666') return Response.json({ error: 'provider rejected' }, { status: 409 }); 
      return Response.json({ gatewayRefundId: `rfnd_${key}`, status: 'PENDING' });
    }
    if (url.pathname === '/__seen') return Response.json(seen);
    return new Response('nf', { status: 404 });
  },
});
console.log('fake gateway on :8083');