import express, { type Express } from "express";
import helmet from "helmet";
import { requestContext } from "./middleware/requestContext";
import { accessLog } from "./middleware/accessLog";
import { notFound } from "./middleware/notFound";
import { errorHandler } from "./middleware/errorHandler";

export default function createApp(): Express {
  const app: Express = express();

  app.disable("x-powered-by");
  app.set("trust proxy", 1);

  app.use(helmet());
  app.use(express.json({ limit: "1mb" }));

  app.use(requestContext);
  app.use(accessLog);

  //   app.use(healthRouter);

  //   app.use("/v1/admin", adminRouter);

    app.use(notFound);
    app.use(errorHandler);
  
  return app;
}
