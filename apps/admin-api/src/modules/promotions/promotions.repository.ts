import type { Prisma, Currency } from '@payrail/db';
import { prisma } from '../../lib/prisma';

class PromotionsRepository {
  create(data: Prisma.PromotionsCreateInput, tx: Prisma.TransactionClient | typeof prisma = prisma) { 
    return tx.promotions.create({
      data,
      include: { rules: true, effects: true, budgets: true, coupons: true },
    });
  }

  findById(id: string) {
    return prisma.promotions.findUnique({
      where: { id },
      include: { rules: true, effects: true, coupons: true, budgets: true },
    });
  }

  listAll() {
    return prisma.promotions.findMany({
      orderBy: [{ priority: 'desc' }, { createdAt: 'desc' }],
      include: { effects: true, _count: { select: { coupons: true } } },
    });
  }

  async supportedCurrencies(): Promise<Currency[]> {
    const rows = await prisma.supportedCurrency.findMany({ where: { enabled: true }, select: { code: true } });
    return rows.map((r) => r.code);
  }

  update(id: string, data: Prisma.PromotionsUpdateInput, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.promotions.update({ where: { id }, data, include: { rules: true, effects: true } });
  }

  upsertBudget(promotionId: string, currency: Currency, capMinor: bigint, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.promotionBudget.upsert({
      where: { promotionId_currency: { promotionId, currency } },
      create: { promotion: { connect: { id: promotionId } }, currency, capMinor },
      update: { capMinor },
    });
  }

  listBudgets(promotionId: string, currency?: Currency) {
    return prisma.promotionBudget.findMany({
      where: { promotionId, ...(currency ? { currency } : {}) },
      orderBy: { currency: 'asc' },
    });
  }

  async spentByCurrency(promotionId: string): Promise<Map<Currency, bigint>> {
    const rows = await prisma.promotionSpend.groupBy({
      by: ['currency'],
      where: { promotionId },
      _sum: { amountMinor: true },
    });
    return new Map(rows.map((r) => [r.currency, r._sum.amountMinor ?? 0n]));
  }

  createCoupon(promotionId: string, data: Omit<Prisma.CouponCodeCreateInput, 'promotion'>, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.couponCode.create({ data: { ...data, promotion: { connect: { id: promotionId } } } });
  }
}

export const promotionsRepository = new PromotionsRepository();