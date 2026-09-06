import type { Prisma } from '@payrail/db';
import { prisma } from '../../lib/prisma';

class DlqRepository {
  async list(args: { source?: string; reason?: string; needsReview?: boolean; cursor?: string; limit: number }) {
    const where: Prisma.DeadLetterEventWhereInput = {
      ...(args.source ? { source: args.source } : {}),
      ...(args.reason ? { reason: { contains: args.reason, mode: 'insensitive' } } : {}),
      ...(args.needsReview !== undefined ? { needsReview: args.needsReview } : {}),
    };
    const rows = await prisma.deadLetterEvent.findMany({
      where,
      orderBy: { createdAt: 'desc' },
      take: args.limit + 1, 
      ...(args.cursor ? { cursor: { id: args.cursor }, skip: 1 } : {}), 
    });
    const page = rows.slice(0, args.limit);
    return { page, nextCursor: rows.length > args.limit ? (page[page.length - 1]?.id ?? null) : null };
  }

  findById(id: string, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.deadLetterEvent.findUnique({ where: { id } });
  }

  async claimReplay(id: string, replayedBy: string, tx: Prisma.TransactionClient): Promise<boolean> {
    const claim = await tx.deadLetterEvent.updateMany({
      where: { id, replayedAt: null },
      data: { replayedAt: new Date(), replayedBy },
    });
    return claim.count === 1;
  }
}

export const dlqRepository = new DlqRepository();