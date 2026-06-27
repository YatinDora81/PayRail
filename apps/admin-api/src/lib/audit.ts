import type { Prisma } from "@repo/db";
import { prisma } from "./prisma";
import { jsonSafe } from "./json";
import { requireContext } from "../context/requestContext";

interface AuditInput {
  action: string; //e.g. "promotion.create" | "refund.issue"
  entityType: string; //  e.g. "Promotions" | "Refund"
  entityId?: string | null;
  before?: unknown;
  after?: unknown;
}

export async function writeAudit(
  input: AuditInput,
  client: Prisma.TransactionClient | typeof prisma = prisma,
): Promise<void> {
  const ctx = requireContext();
  if (!ctx.actor) {
    throw new Error("writeAudit called without an authenticated actor");
  }

  await client.adminAuditLog.create({
    data: {
      actorId: ctx.actor.id,
      action: input.action,
      entityType: input.entityType,
      entityId: input.entityId ?? null,
      before: jsonSafe(input.before) as Prisma.InputJsonValue | undefined,
      after: jsonSafe(input.after) as Prisma.InputJsonObject | undefined,
      ip: ctx.ip,
      userAgent: ctx.userAgent,
    
    },
  });

  ctx.logger.info(
    {
      action: input.action,
      entityType: input.entityType,
      entityId: input.entityId,
    },
    "admin audit",
  );
}
