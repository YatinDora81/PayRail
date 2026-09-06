import type { Prisma } from '@payrail/db';
import { prisma } from '../../lib/prisma';

class CouponsRepository {
  create(data: Prisma.CouponCodeCreateInput, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.couponCode.create({ data });
  }
  findById(id: string, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.couponCode.findUnique({ where: { id } });
  }
  update(id: string, data: Prisma.CouponCodeUpdateInput, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.couponCode.update({ where: { id }, data });
  }
  listByPromotion(promotionId: string) {
    return prisma.couponCode.findMany({ where: { promotionId }, orderBy: { createdAt: 'desc' } });
  }
  async list(args: { skip: number; take: number; isActive?: boolean; promotionId?: string; q?: string }) {
    const where: Prisma.CouponCodeWhereInput = {
      ...(args.isActive !== undefined ? { isActive: args.isActive } : {}),
      ...(args.promotionId ? { promotionId: args.promotionId } : {}),
      ...(args.q ? { code: { contains: args.q, mode: 'insensitive' } } : {}),
    };
    const [data, total] = await Promise.all([
      prisma.couponCode.findMany({ where, skip: args.skip, take: args.take, orderBy: { createdAt: 'desc' } }),
      prisma.couponCode.count({ where }),
    ]);
    return { data, total };
  }
}

export const couponsRepository = new CouponsRepository();