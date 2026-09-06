import type { Prisma, DisputeStatus } from '@payrail/db';
import { prisma } from '../../lib/prisma';

class DisputesRepository {
  findById(id: string, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.dispute.findUnique({ where: { id }, include: { payment: { include: { order: true } } } });
  }
  update(id: string, data: Prisma.DisputeUpdateInput, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.dispute.update({ where: { id }, data });
  }
  async list(args: { skip: number; take: number; status?: DisputeStatus; paymentId?: string; orderId?: string }) {
    const where: Prisma.DisputeWhereInput = {
      ...(args.status ? { status: args.status } : {}),
      ...(args.paymentId ? { paymentId: args.paymentId } : {}),
      ...(args.orderId ? { orderId: args.orderId } : {}),
    };
    const [data, total] = await Promise.all([
      prisma.dispute.findMany({ where, skip: args.skip, take: args.take, orderBy: [{ evidenceDueBy: 'asc' }, { openedAt: 'desc' }] }),
      prisma.dispute.count({ where }),
    ]);
    return { data, total };
  }
}

export const disputesRepository = new DisputesRepository();