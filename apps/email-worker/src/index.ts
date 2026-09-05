import client from 'prom-client';
import http from 'node:http';
import { prisma } from './lib/prisma';
import { startConsumer } from './kafka/consumer';
import { sendTransactional } from './email/send';
import {
  renderReceipt,
  renderRefund,
  type OrderPaid,
  type OrderRefunded,
} from './email/templates';
import { logger } from './lib/logger';
import type { Prisma } from '@payrail/db';


const dlqParked = new client.Counter({
  name: 'payrail_dlq_parked_total', 
  help: 'messages parked to DeadLetterEvent',
});

http.createServer(async (_req, res) => {
  res.setHeader('Content-Type', client.register.contentType);
  res.end(await client.register.metrics());
}).listen(9464);

async function parkToDLQ(topic: string, value: string, reason: string): Promise<void> {
  let payload: Prisma.InputJsonValue;
  let key = '';

  try {
    const parsed = JSON.parse(value) as Record<string, unknown>;
    key = typeof parsed.orderId === 'string' ? parsed.orderId : '';
    payload = parsed as Prisma.InputJsonObject;
  } catch {
    payload = { raw: value };
  }

  await prisma.deadLetterEvent.create({
    data: { source: 'email-worker', topic, key, payload, reason },
  });
  dlqParked.inc();
  logger.error({ topic, reason }, 'email parked to DeadLetterEvent');
}

function isPermanentMailError(err: unknown): boolean {
  const code = (err as { responseCode?: number })?.responseCode;
  return typeof code === 'number' && code >= 500 && code < 600;
}

process.on('unhandledRejection', (reason) => {
  logger.error({ reason }, 'unhandled promise rejection');
});
process.on('uncaughtException', (err) => {
  logger.fatal({ err }, 'uncaught exception');
  process.exit(1);
});

const ORDER_PAID_TOPIC = 'order.paid';
const ORDER_REFUNDED_TOPIC = 'order.refunded';

async function handleMessage(topic: string, value: string): Promise<void> {
  if (topic === ORDER_REFUNDED_TOPIC) {
    const event = JSON.parse(value) as OrderRefunded;
    if (!event.email) {
      logger.warn({ orderId: event.orderId }, 'order.refunded without email — skipping');
      return;
    }
    try {
      await sendTransactional({ template: 'REFUND_DONE', refType: 'REFUND',
        refId: event.refundId, to: event.email, mail: renderRefund(event) });
    } catch (err) {
      if (isPermanentMailError(err)) return parkToDLQ(topic, value, 'permanent-mail-error');
      throw err; 
    }
    logger.info({ orderId: event.orderId, refundId: event.refundId }, 'refund mail processed');
    return;
  }
  
  const event = JSON.parse(value) as OrderPaid;
  if (!event.email) {
    logger.warn({ orderId: event.orderId }, 'order.paid without email — skipping');
    return;
  }
  try {
    await sendTransactional({ template: 'ORDER_PAID', refType: 'ORDER',
      refId: event.orderId, to: event.email, mail: renderReceipt(event) });
  } catch (err) {
    if (isPermanentMailError(err)) return parkToDLQ(topic, value, 'permanent-mail-error');
    throw err; 
  }
  logger.info({ orderId: event.orderId, email: event.email }, 'receipt processed');
}

async function main(): Promise<void> {
  const consumer = await startConsumer([ORDER_PAID_TOPIC, ORDER_REFUNDED_TOPIC], handleMessage);

  const shutdown = async (signal: string): Promise<void> => {
    logger.info({ signal }, 'shutting down');
    await consumer.disconnect();
    process.exit(0);
  };
  process.on('SIGINT', () => void shutdown('SIGINT'));
  process.on('SIGTERM', () => void shutdown('SIGTERM'));

  logger.info('email-worker started');
}

main().catch((err) => {
  logger.fatal({ err }, 'failed to start email-worker');
  process.exit(1);
});