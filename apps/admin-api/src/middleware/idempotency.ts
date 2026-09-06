import type { Request, Response, NextFunction } from 'express';
import { createHash } from 'node:crypto';
import { IdempotencyStatus, Prisma } from '@payrail/db';
import { prisma } from '../lib/prisma';
import { jsonSafe } from '../lib/json';
import { AppError } from '../errors';
import { getContext } from '../context/requestContext';

const TTL_HOURS = 24;

function hashBody(body: unknown): string {
  return createHash('sha256').update(JSON.stringify(jsonSafe(body) ?? {})).digest('hex');
}

const inProgressConflict = (): AppError =>
  AppError.conflict('A request with this Idempotency-Key is still processing', 'IDEMPOTENCY_CONFLICT');

export function idempotency(endpoint: string) {
  return async (req: Request, res: Response, next: NextFunction): Promise<void> => {
    try {
      const key = req.header('idempotency-key');
      if (!key) throw AppError.badRequest('Missing Idempotency-Key header');

      const userId = req.actor?.id;
      if (!userId) {
        throw AppError.internal('idempotency() mounted before auth — actor is required');
      }
      const requestHash = hashBody(req.body); 
      const now = new Date();
      const where = { userId_endpoint_idempotencyKey: { userId, endpoint, idempotencyKey: key } };

      const existing = await prisma.idempotencyRecord.findUnique({ where });
      const reclaimable =
        existing !== null &&
        (existing.status === IdempotencyStatus.FAILED ||
          (existing.status === IdempotencyStatus.IN_PROGRESS && existing.expiresAt < now));

      if (existing && !reclaimable) {
        if (existing.requestHash && existing.requestHash !== requestHash) {
          throw AppError.conflict('Idempotency-Key reused with a different body', 'IDEMPOTENCY_CONFLICT');
        }
        if (existing.status === IdempotencyStatus.DONE && existing.responseStatus != null && existing.responseBody != null) {
          res.status(existing.responseStatus).json(existing.responseBody);
          return;
        }
        throw inProgressConflict();
      }

      const expiresAt = new Date(now.getTime() + TTL_HOURS * 3_600_000);
      if (existing) {
        const takeover = await prisma.idempotencyRecord.updateMany({
          where: {
            userId,
            endpoint,
            idempotencyKey: key,
            OR: [
              { status: IdempotencyStatus.FAILED },
              { status: IdempotencyStatus.IN_PROGRESS, expiresAt: { lt: now } },
            ],
          },
          data: {
            status: IdempotencyStatus.IN_PROGRESS,
            requestHash,
            responseStatus: null,
            responseBody: Prisma.DbNull,
            expiresAt,
          },
        });
        if (takeover.count !== 1) throw inProgressConflict();
      } else {
        try {
          await prisma.idempotencyRecord.create({
            data: { idempotencyKey: key, userId, endpoint, requestHash, status: IdempotencyStatus.IN_PROGRESS, expiresAt },
          });
        } catch (e) {
          if (e instanceof Prisma.PrismaClientKnownRequestError && e.code === 'P2002') throw inProgressConflict();
          throw e;
        }
      }

      const originalJson = res.json.bind(res);
      res.json = (body: unknown) => {
        const failed = res.statusCode >= 500;
        void prisma.idempotencyRecord
          .update({
            where,
            data: failed
              ? { status: IdempotencyStatus.FAILED }
              : { status: IdempotencyStatus.DONE, responseStatus: res.statusCode, responseBody: jsonSafe(body) as Prisma.InputJsonValue },
          })
          .catch((e) => getContext()?.logger.error({ err: e }, 'failed to persist idempotent response'));
        return originalJson(body);
      };

      next();
    } catch (err) {
      next(err);
    }
  };
}