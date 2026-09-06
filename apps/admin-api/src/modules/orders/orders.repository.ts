import { LedgerReferenceType, type Prisma, type OrderStatus, type Gateway } from '@payrail/db';
import { prisma } from '../../lib/prisma';

class OrdersRepository {
  findById(id: string) {
    return prisma.order.findUnique({
      where: { id },
      include: {
        payments: true,
        refunds: { orderBy: { createdAt: 'asc' } },
        disputes: true,
        appliedDiscounts: true,
        usages: true,
        invoice: true,
      },
    });
  }
 
  async ledger(orderId: string, refundIds: string[], disputeIds: string[]) {
    return prisma.creditsLedger.findMany({
      where: {
        OR: [
          { referenceType: LedgerReferenceType.ORDER, referenceId: orderId },
          ...(refundIds.length ? [{ referenceType: LedgerReferenceType.REFUND, referenceId: { in: refundIds } }] : []),
          ...(disputeIds.length ? [{ referenceType: LedgerReferenceType.DISPUTE, referenceId: { in: disputeIds } }] : []),
        ],
      },
      orderBy: { createdAt: 'asc' },
    });
  }

  async list(args: { skip: number; take: number; status?: OrderStatus; userId?: string; gateway?: Gateway; gatewayOrderId?: string; from?: Date; to?: Date }) {
    const where: Prisma.OrderWhereInput = {
      ...(args.status ? { status: args.status } : {}),
      ...(args.userId ? { userId: args.userId } : {}),
      ...(args.gateway ? { gateway: args.gateway } : {}),
      ...(args.gatewayOrderId ? { gatewayOrderId: args.gatewayOrderId } : {}),
      ...(args.from || args.to ? { createdAt: { ...(args.from ? { gte: args.from } : {}), ...(args.to ? { lte: args.to } : {}) } } : {}),
    };
    const [data, total] = await Promise.all([
      prisma.order.findMany({ where, skip: args.skip, take: args.take, orderBy: { createdAt: 'desc' } }),
      prisma.order.count({ where }),
    ]);
    return { data, total };
  }
}

export const ordersRepository = new OrdersRepository();