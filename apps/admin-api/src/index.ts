import { runClustered } from "./cluster";
import { logger } from "./lib/logger";
import { createServer } from "./server";

process.on("unhandledRejection", (reason) => {
  logger.error({ reason }, "unhandled promise rejection");
});
process.on("uncaughtException", (err) => {
  logger.fatal({ err }, "uncaught exception");
  process.exit(1);
});

runClustered(createServer).catch((err) => {
  logger.fatal({ err }, "failed to start admin-api");
  process.exit(1);
});
