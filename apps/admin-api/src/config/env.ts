import { z } from 'zod';

const EnvSchema = z.object({
  NODE_ENV: z.enum(['development', 'test', 'production']).default('development'),
  PORT: z.coerce.number().int().positive().default(4001),
  LOG_LEVEL: z.enum(['fatal', 'error', 'warn', 'info', 'debug', 'trace', 'silent']).default('info'),
  DATABASE_URL: z.url(),
  ADMIN_JWT_SECRET: z.string().min(16, 'ADMIN_JWT_SECRET must be at least 16 chars'),
  ADMIN_ACTOR_CACHE_TTL_S: z.coerce.number().int().min(0).default(30),
  GATEWAY_URL: z.url(),
  GATEWAY_TIMEOUT_MS: z.coerce.number().int().positive().default(8000),
  REDIS_URL: z.url().default('redis://localhost:6379'),
  CLUSTER_ENABLED: z.enum(['true', 'false']).default('false').transform((v) => v === 'true'),
  CLUSTER_WORKERS: z.coerce.number().int().min(0).default(0),
});

const parsed = EnvSchema.safeParse(process.env);
if (!parsed.success) {
  console.error('Invalid environment configuration:', z.flattenError(parsed.error).fieldErrors);
  process.exit(1);
}

export const env = parsed.data;
export const isProd = env.NODE_ENV === 'production';