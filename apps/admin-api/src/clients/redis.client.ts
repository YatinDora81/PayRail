import Redis from "ioredis";
import { env } from "../config/env";
import { logger } from "../lib/logger";

export const redis = new Redis(env.REDIS_URL, {
  maxRetriesPerRequest: 2,
  enableReadyCheck: true,
  // A Redis blip must not take admin-api down: cache/lock callers treat any
  // rejected command as a miss and fall through to Postgres (see lib/cache.ts).
  retryStrategy: (times) => Math.min(times * 200, 2_000),
});

redis.on("error", (err) => logger.error({ err }, "redis error"));
redis.on("connect", () => logger.info("redis connected"));

export async function closeRedis(): Promise<void> {
  try {
    await redis.quit();
  } catch {
    redis.disconnect();
  }
}
