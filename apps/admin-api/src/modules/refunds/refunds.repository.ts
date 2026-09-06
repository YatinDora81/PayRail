import type { Prisma } from '@payrail/db';
import { prisma } from '../../lib/prisma';

class RefundsRepository {
  findPaymentWithRefunds(paymentId: string, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.payment.findUnique({ where: { id: paymentId }, include: { refunds: true } });
  }

  create(data: Prisma.RefundCreateInput, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.refund.create({ data });
  }

  findByIdempotencyKey(idempotencyKey: string, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.refund.findUnique({ where: { idempotencyKey } });
  }

  findById(id: string) {
    return prisma.refund.findUnique({ where: { id }, include: { payment: true } });
  }
}

export const refundsRepository = new RefundsRepository();