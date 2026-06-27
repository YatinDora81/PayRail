import type { Request, Response, NextFunction } from "express";
import { AppError } from "../errors";
import { createHash } from "crypto";
import { jsonSafe } from "../lib/json";
import { prisma } from "@repo/db";
import { getContext } from "../context/requestContext";

const TTL_HOURS = 24;

function hashBody(body: unknown): string {
  return createHash("sha256")
    .update(JSON.stringify(jsonSafe(body) ?? {}))
    .digest("hex");
}

export function idempotency(endpoint: string) {
  return async (req: Request, res: Response, next: NextFunction) => {
    try {
      const key = req.header("idempotency-key");
      if (!key) throw AppError.badRequest("Missing Idempotency-Key header");

      const userId = req.actor?.id ?? null;
      const requestHash = hashBody(req.body);
      const where = {
        userId_endpoint_idempotencyKey: {
          userId,
          endpoint,
          idempotencyKey: key,
        },
      };

      const existing = await prisma.idempotencyRecord.findUnique({ where });
      if (existing) {
        if (existing.requestHash && existing.requestHash !== requestHash) {
          throw AppError.conflict(
            "Idempotency-Key reused with a different body",
            "IDEMPOTENCY_CONFLICT",
          );
        }
        if (existing.responseStatus != null && existing.responseBody != null) {
          res.status(existing.responseStatus).json(existing.responseBody);
          return;
        }
        throw AppError.conflict(
          "A request with this Idempotency-Key is still processing",
          "IDEMPOTENCY_CONFLICT",
        );
      }

      try {
        await prisma.idempotencyRecord.create({
          data: {
            idempotencyKey: key,
            userId,
            endpoint,
            requestHash,
            expiresAt: new Date(Date.now() + TTL_HOURS * 3_600_000),
          },
        });
      } catch {
        throw AppError.conflict(
          "A request with this Idempotency-Key is already in progress",
          "IDEMPOTENCY_CONFLICT",
        );

        const originalJson = res.json.bind(res);
        res.json = (body: unknown) => {
          void prisma.idempotencyRecord
            .update({
              where,
              data: {
                responseStatus: res.statusCode,
                responseBody: jsonSafe(body) as object,
              },
            })
            .catch((e) =>
              getContext()?.logger.error(
                { err: e },
                "failed to persist idempotent response",
              ),
            );
          return originalJson(body);
        };

        next();
      }
    } catch (error) {
      next(error);
    }
  };
}
