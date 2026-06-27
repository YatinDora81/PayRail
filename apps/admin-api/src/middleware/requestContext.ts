import type { Request, Response, NextFunction } from "express";
import { randomUUID } from "crypto";
import { trace } from "@opentelemetry/api";
import { runWithContext } from "../context/requestContext";
import { logger } from "../lib/logger";

function resolveTraceId(req: Request): string {
  const active = trace.getActiveSpan()?.spanContext()?.traceId;
  if (active) return active;

  const traceParent = req.header("traceparent");
  if (traceParent) {
    const parts = traceParent.split("-");
    if (parts.length >= 2 && parts[1] && /^[0-9a-f]{32}$/i.test(parts[1]))
      return parts[1];
  }

  const requestId = req.header("x-request-id");
  if (requestId && requestId.length <= 200) return requestId;
  return randomUUID();
}

export function requestContext(
  req: Request,
  res: Response,
  next: NextFunction,
): void {
  const traceId = resolveTraceId(req);
  req.traceId = traceId;
  res.setHeader("x-trace-id", traceId);

  const reqLogger = logger.child({
    traceId,
    method: req.method,
    path: req.path,
  });

  runWithContext(
    {
      traceId,
      ip: req?.ip,
      userAgent: req.header("user-agent"),
      logger: reqLogger,
    },
    () => next(),
  );
}
