import type { Server } from "http";
import createApp from "./app";
import { env } from "./config/env";
import { logger } from "./lib/logger";
import { prisma } from "./lib/prisma";
import { closeRedis } from "./clients/redis.client";
import { disconnectProducer } from "./clients/kafka.producer";

BigInt.prototype.toJSON = function (): string {
  return this.toString();
};

export async function createServer(): Promise<Server> {
  await prisma.$connect();
  const app = createApp();
  const server = app.listen(env.PORT, () => {
    logger.info(
      { port: env.PORT, env: env.NODE_ENV },
      "admin-api listening...",
    );
  });

  const shutdown = async (signal: string) => {
    logger.info({ signal }, "shutting down");
    server.close(() => {
      void Promise.allSettled([
        prisma.$disconnect(),
        closeRedis(),
        disconnectProducer(),
      ]).finally(() => {
        logger.info("shutdown complete");
        process.exit(0);
      });
    });
    setTimeout(() => process.exit(1), 10_000).unref();
  };

  process.on("SIGTERM", () => shutdown("SIGTERM"));
  process.on("SIGINT", () => shutdown("SIGINT"));

  return server;
}
