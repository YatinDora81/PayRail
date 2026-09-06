import type { Prisma, CardNetwork } from '@payrail/db';
import { prisma } from '../../lib/prisma';

class BinRangesRepository {
  create(data: Prisma.BinRangeCreateInput, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.binRange.create({ data });
  }
  findById(id: string, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.binRange.findUnique({ where: { id } });
  }
  async list(args: { skip: number; take: number; isActive?: boolean; network?: CardNetwork; bankName?: string }) {
    const where: Prisma.BinRangeWhereInput = {
      ...(args.isActive !== undefined ? { isActive: args.isActive } : {}),
      ...(args.network ? { network: args.network } : {}),
      ...(args.bankName ? { bankName: { contains: args.bankName, mode: 'insensitive' } } : {}),
    };
    const [data, total] = await Promise.all([
      prisma.binRange.findMany({ where, skip: args.skip, take: args.take, orderBy: { binLow: 'asc' } }),
      prisma.binRange.count({ where }),
    ]);
    return { data, total };
  }
}

export const binRangesRepository = new BinRangesRepository();