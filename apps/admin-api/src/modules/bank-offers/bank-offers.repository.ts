import type { Prisma } from '@payrail/db';
import { prisma } from '../../lib/prisma';

class BankOffersRepository {
  create(data: Prisma.BankOfferCreateInput, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.bankOffer.create({ data });
  }
  findById(id: string, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.bankOffer.findUnique({ where: { id } });
  }
  update(id: string, data: Prisma.BankOfferUpdateInput, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.bankOffer.update({ where: { id }, data });
  }
  async list(args: { skip: number; take: number; isActive?: boolean; bankName?: string; country?: string }) {
    const where: Prisma.BankOfferWhereInput = {
      ...(args.isActive !== undefined ? { isActive: args.isActive } : {}),
      ...(args.bankName ? { bankName: { contains: args.bankName, mode: 'insensitive' } } : {}),
      ...(args.country ? { country: { in: [args.country, ''] } } : {}),  
    };
    const [data, total] = await Promise.all([
      prisma.bankOffer.findMany({ where, skip: args.skip, take: args.take, orderBy: { createdAt: 'desc' } }),
      prisma.bankOffer.count({ where }),
    ]);
    return { data, total };
  }
}

export const bankOffersRepository = new BankOffersRepository();