import type { Request, Response, NextFunction } from "express";
import { getContext } from "../context/requestContext";

export function accessLog(
  _req: Request,
  res: Response,
  next: NextFunction,
): void {
  const start = process.hrtime.bigint();
  res.on("finish", () => {
    const durationMs = Number(process.hrtime.bigint() - start) / 1e6;
    getContext()?.logger.info(
      {
        status: res.statusCode,
        durationMs: Math.round(durationMs * 100) / 100,
      },
      "request completed",
    );
  });
  next();
}
