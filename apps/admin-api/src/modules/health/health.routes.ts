import { Router } from 'express';
import { collectDefaultMetrics, register } from 'prom-client';
import { asyncHandler } from '../../lib/asyncHandler';
import { prisma } from '../../lib/prisma';
import { redis } from '../../clients/redis.client';

export const healthRouter : Router = Router();

collectDefaultMetrics(); 

const REDIS_PING_MS = 500;

async function pingRedis(): Promise<'ok' | 'degraded'> {
  try {
    const pong = await Promise.race([
      redis.ping(),
      new Promise<never>((_, reject) => setTimeout(() => reject(new Error('redis ping timeout')), REDIS_PING_MS).unref()),
    ]);
    return pong === 'PONG' ? 'ok' : 'degraded';
  } catch {
    return 'degraded';
  }
}

healthRouter.get('/healthz', (_req, res) => {
  res.json({ status: 'ok' });
});

healthRouter.get(
  '/readyz',
  asyncHandler(async (_req, res) => {
    const [postgres, redisStatus] = await Promise.all([
      prisma.$queryRaw`SELECT 1`.then(() => 'ok' as const).catch(() => 'down' as const),
      pingRedis(),
    ]);
    const checks = { postgres, redis: redisStatus };
    if (postgres !== 'ok') {
      res.status(503).json({ status: 'unavailable', checks });
      return;
    }
    res.json({ status: 'ready', checks });
  }),
);

healthRouter.get(
  '/metrics',
  asyncHandler(async (_req, res) => {
    res.set('Content-Type', register.contentType);
    res.send(await register.metrics());
  }),
);