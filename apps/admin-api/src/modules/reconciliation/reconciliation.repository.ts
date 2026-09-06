import type { Prisma, Currency } from '@payrail/db';
import { prisma } from '../../lib/prisma';

class ReconciliationRepository {
  findById(id: string) {
    return prisma.reconciliationLog.findUnique({ where: { id } });
  }
  async list(args: { skip: number; take: number; kind?: string; promotionId?: string; currency?: Currency; corrected?: boolean; deadLetterId?: string; from?: Date; to?: Date }) {
    const where: Prisma.ReconciliationLogWhereInput = {
      ...(args.kind ? { kind: args.kind } : {}),
      ...(args.promotionId ? { promotionId: args.promotionId } : {}),
      ...(args.currency ? { currency: args.currency } : {}),
      ...(args.corrected !== undefined ? { corrected: args.corrected } : {}),
      ...(args.deadLetterId ? { deadLetterId: args.deadLetterId } : {}),
      ...(args.from || args.to ? { createdAt: { ...(args.from ? { gte: args.from } : {}), ...(args.to ? { lte: args.to } : {}) } } : {}),
    };
    const [data, total] = await Promise.all([
      prisma.reconciliationLog.findMany({ where, skip: args.skip, take: args.take, orderBy: { createdAt: 'desc' } }),
      prisma.reconciliationLog.count({ where }),
    ]);
    return { data, total };
  }
}

export const reconciliationRepository = new ReconciliationRepository();