import type { Prisma, InvoiceStatus } from '@payrail/db';
import { prisma } from '../../lib/prisma';

class InvoicesRepository {
  findById(id: string) {
    return prisma.invoice.findUnique({ where: { id }, include: { order: true } });
  }
  findByOrderId(orderId: string) {
    return prisma.invoice.findUnique({ where: { orderId } });
  }
  async list(args: { skip: number; take: number; status?: InvoiceStatus; orderId?: string; series?: string; issuedFrom?: Date; issuedTo?: Date }) {
    const where: Prisma.InvoiceWhereInput = {
      ...(args.status ? { status: args.status } : {}),
      ...(args.orderId ? { orderId: args.orderId } : {}),
      ...(args.series ? { series: args.series } : {}),
      ...(args.issuedFrom || args.issuedTo
        ? { issuedAt: { ...(args.issuedFrom ? { gte: args.issuedFrom } : {}), ...(args.issuedTo ? { lte: args.issuedTo } : {}) } }
        : {}),
    };
    const [data, total] = await Promise.all([
      prisma.invoice.findMany({ where, skip: args.skip, take: args.take, orderBy: [{ series: 'asc' }, { number: 'desc' }] }),
      prisma.invoice.count({ where }),
    ]);
    return { data, total };
  }
}

export const invoicesRepository = new InvoicesRepository();