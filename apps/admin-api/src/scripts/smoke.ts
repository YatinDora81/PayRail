import jwt from 'jsonwebtoken';
import Redis from 'ioredis';
import { prisma } from '@payrail/db';

const SECRET = process.env.ADMIN_JWT_SECRET ?? 'change-me-admin-secret-16';
const A = process.env.ADMIN_API_URL ?? 'http://localhost:4001';
const FAKE = process.env.FAKE_GATEWAY_URL ?? 'http://localhost:8083';
const redis = new Redis(process.env.REDIS_URL ?? 'redis://localhost:6379');
const R = Date.now().toString(36).toUpperCase();

console.log('run', R);
const owner = jwt.sign({ sub: 'adm_dev_owner' }, SECRET, { algorithm: 'HS256', expiresIn: '1h' });
const call = async (method: string, path: string, body?: unknown, extra: Record<string, string> = {}, token = owner) => {
  const r = await fetch(`${A}${path}`, { method, headers: { 'content-type': 'application/json', authorization: `Bearer ${token}`, ...extra }, body: body ? JSON.stringify(body) : undefined, redirect: 'manual' });
  const text = await r.text(); let j: any = null; try { j = JSON.parse(text); } catch {}
  return { status: r.status, j, text, headers: r.headers };
};
const rows: string[][] = [];
const check = (name: string, ok: boolean, detail = '') => rows.push([ok ? 'PASS' : 'FAIL', name, detail]);

// ── promotions: create → activate → budgets live view → outbox rows ──
const promo = await call('POST', '/v1/admin/promotions', { name: 'smoke-drop', startsAt: new Date(Date.now() - 3600e3), endsAt: new Date(Date.now() + 3600e3), effects: [{ effectType: 'PERCENT_BPS', valueBps: 1000 }], budgets: [{ currency: 'INR', capMinor: '10000000' }], coupons: [{ code: `SMOKE10-${R}`, perUserLimit: 0 }] });
check('POST /promotions → 201, inactive, coupon perUserLimit 0 accepted', promo.status === 201 && promo.j.data.isActive === false && promo.j.data.coupons[0].perUserLimit === 0, `${promo.status}`);
const pid = promo.j.data.id;
const bad = await call('POST', '/v1/admin/promotions', { name: 'x', startsAt: new Date(), endsAt: new Date(Date.now() - 1), effects: [] });
check('POST /promotions bad body → 422 VALIDATION_ERROR', bad.status === 422 && bad.j.error.code === 'VALIDATION_ERROR');
const eur = await call('POST', '/v1/admin/promotions', { name: 'x', startsAt: new Date(), endsAt: new Date(Date.now() + 1e6), effects: [{ effectType: 'PERCENT_BPS', valueBps: 1 }], budgets: [{ currency: 'EUR', capMinor: '1' }] });
check('budget in a disabled currency (EUR) → 400', eur.status === 400, eur.j?.error?.message);
const act = await call('POST', `/v1/admin/promotions/${pid}/activate`);
check('POST /activate → 200 isActive', act.status === 200 && act.j.data.isActive === true);
const act2 = await call('POST', `/v1/admin/promotions/${pid}/activate`);
check('activate again → idempotent 200', act2.status === 200);
let b = await call('GET', `/v1/admin/promotions/${pid}/budgets?currency=INR`);
check('GET /budgets?currency=INR (unseeded) → spentMinor "0", remainingMinor null, seeded false', b.status === 200 && b.j.data.spentMinor === '0' && b.j.data.remainingMinor === null && b.j.data.seeded === false, JSON.stringify(b.j.data));
await redis.set(`promo:budget:${pid}:INR`, '9750000'); // what the reconciler does after the arm event
b = await call('GET', `/v1/admin/promotions/${pid}/budgets?currency=INR`);
check('GET /budgets after the reconciler seeds → remainingMinor "9750000", seeded true', b.j.data.remainingMinor === '9750000' && b.j.data.seeded === true);
const put = await call('PUT', `/v1/admin/promotions/${pid}/budgets`, { currency: 'INR', capMinor: '20000000' });
check('PUT /budgets on an ACTIVE promo → 200 cap raised', put.status === 200 && put.j.data.capMinor === '20000000');
const all = await call('GET', `/v1/admin/promotions/${pid}/budgets`);
check('GET /budgets (no currency) → array', Array.isArray(all.j.data) && all.j.data.length === 1);
const missing = await call('GET', `/v1/admin/promotions/${pid}/budgets?currency=USD`);
check('GET /budgets?currency=USD (no such budget) → 404', missing.status === 404);
const outbox = await prisma.outboxEvent.findMany({ where: { partitionKey: pid }, orderBy: { createdAt: 'asc' } });
check('outbox: promotion.activated + promotion.budget.upserted rows committed with the writes', outbox.map((o) => o.topic).join(',') === 'promotion.activated,promotion.budget.upserted', outbox.map((o) => o.topic).join(','));
check('outbox: capMinor is a STRING (Go event type) and the request traceId is stored in headers', (outbox[1]?.payload as any)?.capMinor === '20000000' && typeof (outbox[1]?.headers as any)?.traceId === 'string', JSON.stringify(outbox[1]?.headers));
const W3C = '0af7651916cd43dd8448eb211c80319c';
await call('PUT', `/v1/admin/promotions/${pid}/budgets`, { currency: 'INR', capMinor: '30000000' }, { traceparent: `00-${W3C}-b7ad6b7169203331-01` });
const traced = await prisma.outboxEvent.findFirst({ where: { partitionKey: pid, topic: 'promotion.budget.upserted' }, orderBy: { createdAt: 'desc' } });
check('outbox: an inbound W3C traceparent is continued in headers.traceparent + traceId', (traced?.headers as any)?.traceId === W3C && ((traced?.headers as any)?.traceparent as string).includes(W3C), JSON.stringify(traced?.headers));
const get1 = await call('GET', `/v1/admin/promotions/${pid}`);
const get2 = await call('GET', `/v1/admin/promotions/${pid}`);
check('GET /promotions/:id twice → cached (Redis key exists), same capMinor', get1.status === 200 && get2.j.data.budgets[0].capMinor === '30000000' && (await redis.exists(`promotions:item:${pid}`)) === 1);
const list = await call('GET', '/v1/admin/promotions?isActive=true&q=smoke');
check('GET /promotions?isActive=true&q=smoke → paginated, every row active + matching', list.j.pagination?.total >= 1 && list.j.data.every((p: any) => p.isActive && /smoke/i.test(p.name)), `total=${list.j.pagination?.total}`);
const upd = await call('PATCH', `/v1/admin/promotions/${pid}`, { endsAt: new Date(Date.now() - 7200e3) });
check('PATCH with endsAt before the stored startsAt → 400', upd.status === 400);

// ── coupons / plans / bank-offers / bin-ranges ──
const cp = await call('POST', '/v1/admin/coupons', { code: `STANDALONE-${R}`, promotionId: pid, perUserLimit: 0 });
check('POST /coupons bound to the promotion → 201', cp.status === 201);
const cpDup = await call('POST', '/v1/admin/coupons', { code: `STANDALONE-${R}`, promotionId: pid });
check('duplicate coupon code → 409 CONFLICT (P2002 mapped)', cpDup.status === 409);
const cpList = await call('GET', `/v1/admin/promotions/${pid}/coupons`);
check('GET /promotions/:id/coupons → both coupons', cpList.j.data.length === 2);
const plan = await call('POST', '/v1/admin/plans', { name: 'Smoke Pack', credits: 50, prices: [{ country: 'IN', currency: 'INR', amountMinor: '19900' }] });
check('POST /plans → 201 with geo price row', plan.status === 201 && plan.j.data.prices[0].amountMinor === '19900');
const planUpd = await call('PATCH', `/v1/admin/plans/${plan.j.data.id}`, { prices: [{ country: 'IN', currency: 'INR', amountMinor: '24900' }, { country: 'US', currency: 'USD', amountMinor: '299' }] });
check('PATCH /plans prices fully replaced', planUpd.j.data.prices.length === 2);
const bin = await call('POST', '/v1/admin/bin-ranges', { bankName: 'HDFC', network: 'VISA', binLow: '411111', binHigh: '411199', cardType: 'CREDIT' });
check('POST /bin-ranges → 201', bin.status === 201);
const bo = await call('POST', '/v1/admin/bank-offers', { bankName: 'HDFC', cardNetwork: 'VISA', binRangeId: bin.j.data.id, description: '10% off', country: 'IN', discountBps: 1000, currency: 'INR', startsAt: new Date(), endsAt: new Date(Date.now() + 1e6) });
check('POST /bank-offers with binRangeId (relation connect) + description/country → 201', bo.status === 201 && bo.j.data.binRangeId === bin.j.data.id && bo.j.data.country === 'IN', bo.text.slice(0, 120));
const boDetach = await call('PATCH', `/v1/admin/bank-offers/${bo.j.data.id}`, { binRangeId: null });
check('PATCH /bank-offers binRangeId:null → disconnected', boDetach.status === 200 && boDetach.j.data.binRangeId === null, boDetach.text.slice(0, 120));
const boBadBin = await call('PATCH', `/v1/admin/bank-offers/${bo.j.data.id}`, { binRangeId: 'nope' });
check('PATCH /bank-offers unknown binRangeId → 404', boBadBin.status === 404);

// ── orders (read-only) + refunds through the facade + idempotency ──
await prisma.user.upsert({ where: { id: 'u_dev' }, create: { id: 'u_dev', email: 'dev@payrail.dev' }, update: {} });
await prisma.order.create({ data: { id: `ord_smoke-${R}`, idempotencyKey: `ik_smoke-${R}`, userId: 'u_dev', planId: 'plan_starter', status: 'PAID', currency: 'INR', baseAmountMinor: 49900n, finalAmountMinor: 49900n, creditsGranted: 100, gateway: 'RAZORPAY', gatewayOrderId: `order_smoke-${R}`, expiresAt: new Date(Date.now() + 1e6), paidAt: new Date(), payments: { create: { id: `pay_smoke-${R}`, gateway: 'RAZORPAY', gatewayPaymentId: `pay_rzp_smoke-${R}`, amountMinor: 49900n, currency: 'INR', status: 'CAPTURED' } } } });
await prisma.creditsLedger.create({ data: { userId: 'u_dev', delta: 100, reason: 'PURCHASE', referenceType: 'ORDER', referenceId: `ord_smoke-${R}` } });
await prisma.invoice.create({ data: { orderId: `ord_smoke-${R}`, series: `INR${R}`, number: 1, currency: 'INR', amountMinor: 49900n, taxBps: 1800 } });
const ord = await call('GET', `/v1/admin/orders/ord_smoke-${R}`);
check('GET /orders/:id → status PAID with payments + invoiceNumber', ord.status === 200 && ord.j.data.status === 'PAID' && ord.j.data.payments.length === 1 && ord.j.data.invoice.invoiceNumber === `INV-INR${R}-000001`, ord.text.slice(0, 100));
const led = await call('GET', `/v1/admin/orders/ord_smoke-${R}/ledger`);
check('GET /orders/:id/ledger → the PURCHASE row', led.j.data.length === 1 && led.j.data[0].reason === 'PURCHASE');
const inv = await call('GET', `/v1/admin/orders/ord_smoke-${R}/invoice`);
check('GET /orders/:id/invoice → invoiceNumber matches /^INV-/ (e2e contract)', /^INV-/.test(inv.j.data.invoiceNumber) && inv.j.data.number === 1);
check('GET /orders/unknown → 404', (await call('GET', '/v1/admin/orders/nope')).status === 404);
const invList = await call('GET', `/v1/admin/invoices?series=INR${R}`);
check('GET /invoices?series=INR → 1 with invoiceNumber', invList.j.pagination.total === 1 && invList.j.data[0].invoiceNumber === `INV-INR${R}-000001`);
const pdf = await call('GET', `/v1/admin/invoices/${invList.j.data[0].id}/pdf`);
check('GET /invoices/:id/pdf with no rendered PDF → 404 (never an empty file)', pdf.status === 404);

const rf1 = await call('POST', '/v1/admin/refunds', { paymentId: `pay_smoke-${R}`, amountMinor: '10000', reason: 'smoke' }, { 'idempotency-key': `smoke-key-1-${R}` });
check('POST /refunds → 201 PENDING with gatewayRefundId from the facade, status NOT PROCESSED', rf1.status === 201 && rf1.j.data.status === 'PENDING' && rf1.j.data.gatewayRefundId === `rfnd_smoke-key-1-${R}`, rf1.text.slice(0, 160));
const rf1b = await call('POST', '/v1/admin/refunds', { paymentId: `pay_smoke-${R}`, amountMinor: '10000', reason: 'smoke' }, { 'idempotency-key': `smoke-key-1-${R}` });
check('replay same key+body → identical 201 (stored response), facade called once', rf1b.status === 201 && rf1b.j.data.id === rf1.j.data.id && ((await (await fetch(`${FAKE}/__seen`)).json()) as any[]).filter((c) => c.key === `smoke-key-1-${R}`).length === 1);
const rf1c = await call('POST', '/v1/admin/refunds', { paymentId: `pay_smoke-${R}`, amountMinor: '20000' }, { 'idempotency-key': `smoke-key-1-${R}` });
check('same key, different body → 409 IDEMPOTENCY_CONFLICT', rf1c.status === 409 && rf1c.j.error.code === 'IDEMPOTENCY_CONFLICT');
const rfNoKey = await call('POST', '/v1/admin/refunds', { paymentId: `pay_smoke-${R}`, amountMinor: '1' });
check('missing Idempotency-Key → 400', rfNoKey.status === 400);
const rfOver = await call('POST', '/v1/admin/refunds', { paymentId: `pay_smoke-${R}`, amountMinor: '49900' }, { 'idempotency-key': `smoke-key-2-${R}` });
check('refund over the ceiling (10000 PENDING already) → 400 exceeds', rfOver.status === 400 && /exceeds/.test(rfOver.j.error.message));
const rfRej = await call('POST', '/v1/admin/refunds', { paymentId: `pay_smoke-${R}`, amountMinor: '666' }, { 'idempotency-key': `smoke-key-3-${R}` });
const rejRow = await prisma.refund.findUnique({ where: { idempotencyKey: `smoke-key-3-${R}` } });
check('provider 409 rejection → 409 REFUND_REJECTED, row FAILED (ceiling re-opened)', rfRej.status === 409 && rfRej.j.error.code === 'REFUND_REJECTED' && rejRow?.status === 'FAILED');
const rfGet = await call('GET', `/v1/admin/refunds/${rf1.j.data.id}`);
check('GET /refunds/:id → 200', rfGet.status === 200);

// ── dlq · audit · reconciliation · disputes ──
const dl = await prisma.deadLetterEvent.create({ data: { source: 'settlement-worker', topic: 'payment.events', key: `order_smoke-${R}`, payload: { eventId: 'evt_1', kind: 'PAYMENT' }, reason: `stale capture ${R}: order EXPIRED`, needsReview: true } });
const dlq = await call('GET', `/v1/admin/dlq?needsReview=true&source=settlement-worker&reason=${R}`);
check('GET /dlq?needsReview=true → the parked row', dlq.j.data.length === 1 && dlq.j.nextCursor === null);
const rp = await call('POST', `/v1/admin/dlq/${dl.id}/replay`);
const rp2 = await call('POST', `/v1/admin/dlq/${dl.id}/replay`);
const replayed = await prisma.outboxEvent.findFirst({ where: { partitionKey: `order_smoke-${R}`, topic: 'payment.events' } });
check('POST /dlq/:id/replay → 202 + outbox row; second replay → 409 ALREADY_REPLAYED', rp.status === 202 && replayed !== null && rp2.status === 409 && rp2.j.error.code === 'ALREADY_REPLAYED');
const audit = await call('GET', '/v1/admin/audit?entityType=DeadLetterEvent');
check('GET /audit?entityType=DeadLetterEvent → dlq.replay audited with actorId', audit.j.data[0]?.action === 'dlq.replay' && audit.j.data[0]?.actorId === 'adm_dev_owner');
const rec = await call('GET', '/v1/admin/reconciliation');
check('GET /reconciliation → 200 empty', rec.status === 200 && rec.j.pagination.total === 0);
const dsp = await prisma.dispute.create({ data: { orderId: `ord_smoke-${R}`, paymentId: `pay_smoke-${R}`, gateway: 'RAZORPAY', gatewayDisputeId: `dsp_smoke-${R}`, amountMinor: 49900n, currency: 'INR' } });
const ev = await call('PATCH', `/v1/admin/disputes/${dsp.id}/evidence`, { evidence: { receipt: 'r1' }, note: 'shipped on time' });
check('PATCH /disputes/:id/evidence → UNDER_REVIEW, note persisted', ev.j.data.status === 'UNDER_REVIEW' && ev.j.data.note === 'shipped on time', ev.text.slice(0, 100));
const ev2 = await call('PATCH', `/v1/admin/disputes/${dsp.id}/evidence`, { evidence: { x: 1 } });
check('evidence again → 409 DISPUTE_NOT_CONTESTABLE', ev2.status === 409 && ev2.j.error.code === 'DISPUTE_NOT_CONTESTABLE');
const acc = await call('PATCH', `/v1/admin/disputes/${dsp.id}/accept`, { note: 'conceded' });
check('PATCH /disputes/:id/accept → ACCEPTED with resolvedAt', acc.j.data.status === 'ACCEPTED' && typeof acc.j.data.resolvedAt === 'string');

// ── RBAC + actor cache ──
await prisma.adminUser.upsert({ where: { id: 'adm_ro' }, create: { id: 'adm_ro', email: 'ro@payrail.dev', role: 'READONLY' }, update: {} });
const ro = jwt.sign({ sub: 'adm_ro' }, SECRET, { algorithm: 'HS256', expiresIn: '1h' });
check('READONLY: GET /promotions → 200', (await call('GET', '/v1/admin/promotions', undefined, {}, ro)).status === 200);
check('READONLY: POST /promotions → 403', (await call('POST', '/v1/admin/promotions', {}, {}, ro)).status === 403);
check('READONLY: GET /orders → 403 (SUPPORT floor)', (await call('GET', '/v1/admin/orders', undefined, {}, ro)).status === 403);
check('READONLY: GET /dlq → 403 (ADMIN)', (await call('GET', '/v1/admin/dlq', undefined, {}, ro)).status === 403);
check('actor cached in Redis as admin:{id}', ((await redis.get('admin:adm_ro')) ?? '').includes('"role":"READONLY"'));
const badAlg = jwt.sign({ sub: 'adm_dev_owner' }, SECRET, { algorithm: 'HS512', expiresIn: '1h' });
check('HS512 token → 401 (algorithm pinned)', (await call('GET', '/v1/admin/promotions', undefined, {}, badAlg)).status === 401);
check('unknown route → 404 envelope', (await call('GET', '/v1/admin/nope')).j?.error?.code === 'NOT_FOUND');

await prisma.$disconnect();
redis.disconnect();
const fails = rows.filter((r) => r[0] === 'FAIL').length;
for (const r of rows) console.log(`${r[0]}  ${r[1]}${r[2] ? `   ← ${r[2]}` : ''}`);
console.log(`\n${rows.length - fails}/${rows.length} passed`);
process.exit(fails ? 1 : 0);