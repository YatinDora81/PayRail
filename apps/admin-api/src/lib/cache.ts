import { redis } from '../clients/redis.client';
import { logger } from './logger';

type Box<T> = { v: T; soft: number };  

const LOCK_PX = 3_000; 
const STALE_GRACE_S = 30; 
const WAIT_MS = 60;

const jitter = (ttl: number): number => ttl + Math.floor(Math.random() * Math.max(1, ttl * 0.2));
const lockKey = (k: string): string => `lock:${k}`;

async function acquire(key: string): Promise<boolean> {
  try {
    return (await redis.set(lockKey(key), '1', 'PX', LOCK_PX, 'NX')) === 'OK';
  } catch {
    return false;
  }
}
const release = (key: string): Promise<unknown> => redis.del(lockKey(key)).catch(() => 0);

async function write<T>(key: string, value: T, ttlSeconds: number): Promise<void> {
  const box: Box<T> = { v: value, soft: Date.now() + ttlSeconds * 1000 };
  const hardTtl = jitter(ttlSeconds) + STALE_GRACE_S; 
  await redis.set(key, JSON.stringify(box), 'EX', hardTtl).catch(() => undefined);
}

export async function cached<T>(key: string, ttlSeconds: number, loader: () => Promise<T>): Promise<T> {
  let raw: string | null = null;
  try {
    raw = await redis.get(key);
  } catch {
  }

  if (raw) {
    const box = JSON.parse(raw) as Box<T>;
    if (Date.now() < box.soft) return box.v; 

    if (await acquire(key)) {
      void (async () => {
        try {
          await write(key, await loader(), ttlSeconds);
        } catch (err) {
          logger.warn({ err, key }, 'cache background refresh failed');
        } finally {
          await release(key);
        }
      })();
    }
    return box.v; 
  }

  if (await acquire(key)) {
    try {
      const value = await loader();
      await write(key, value, ttlSeconds);
      return value;
    } finally {
      await release(key);
    }
  }

  await new Promise((r) => setTimeout(r, WAIT_MS)); 
  try {
    const again = await redis.get(key);
    if (again) return (JSON.parse(again) as Box<T>).v;
  } catch {
  }

  return loader(); 
}

export async function get<T>(key: string): Promise<T | null> {
  try {
    const raw = await redis.get(key);
    return raw ? (JSON.parse(raw) as T) : null;
  } catch (err) {
    logger.warn({ err, key }, 'cache get failed');
    return null;
  }
}

export async function set<T>(key: string, value: T, ttlSeconds: number): Promise<void> {
  try {
    await redis.set(key, JSON.stringify(value), 'EX', jitter(ttlSeconds));
  } catch (err) {
    logger.warn({ err, key }, 'cache set failed');
  }
}

export async function del(...keys: string[]): Promise<void> {
  if (keys.length) await redis.del(...keys).catch(() => 0);
}

export const bust = del;