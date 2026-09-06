import type { Prisma } from '@payrail/db';
import { prisma } from '../../lib/prisma';

class AuditRepository {
  findById(id: string) {
    return prisma.adminAuditLog.findUnique({ where: { id } });
  }
  async list(args: { skip: number; take: number; actorId?: string; action?: string; entityType?: string; entityId?: string; from?: Date; to?: Date }) {
    const where: Prisma.AdminAuditLogWhereInput = {
      ...(args.actorId ? { actorId: args.actorId } : {}),
      ...(args.action ? { action: args.action } : {}),
      ...(args.entityType ? { entityType: args.entityType } : {}),
      ...(args.entityId ? { entityId: args.entityId } : {}),
      ...(args.from || args.to ? { createdAt: { ...(args.from ? { gte: args.from } : {}), ...(args.to ? { lte: args.to } : {}) } } : {}),
    };
    const [data, total] = await Promise.all([
      prisma.adminAuditLog.findMany({ where, skip: args.skip, take: args.take, orderBy: { createdAt: 'desc' } }),
      prisma.adminAuditLog.count({ where }),
    ]);
    return { data, total };
  }
}

export const auditRepository = new AuditRepository();