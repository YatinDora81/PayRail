import { availableParallelism } from "node:os";
import cluster from "node:cluster";
import { env } from "./config/env";
import { logger } from "./lib/logger";

export async function runClustered(
  startWorker: () => Promise<unknown>,
): Promise<void> {
  const desired =
    env.CLUSTER_WORKERS > 0 ? env.CLUSTER_WORKERS : availableParallelism();

  if (!env.CLUSTER_ENABLED || desired <= 1) {
    await startWorker();
    return;
  }

  if (cluster.isPrimary) {
    logger.info({ workers: desired }, "cluster primary: forking workers");
    for (let i = 0; i < desired; i++) cluster.fork();

    cluster.on("exit", (worker, code, signal) => {
      logger.error(
        { pid: worker.process.pid, code, signal },
        "worker exited — respawning",
      );
      setTimeout(() => cluster.fork(), 1_000).unref();
    });

    const shutdown = (sig: string): void => {
      logger.info({ sig }, "cluster primary: stopping workers");
      for (const worker of Object.values(cluster.workers ?? {}))
        worker?.kill("SIGTERM");
    };
    process.on("SIGTERM", () => shutdown("SIGTERM"));
    process.on("SIGINT", () => shutdown("SIGINT"));
    return;
  }

  await startWorker();
}
