import type { Prisma } from '@payrail/db';
import { prisma } from '../../lib/prisma';

class PlansRepository {
  create(data: Prisma.PlanCreateInput, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.plan.create({ data, include: { prices: true } });
  }

  findById(id: string, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.plan.findUnique({ where: { id }, include: { prices: true } });
  }

  update(id: string, data: Prisma.PlanUpdateInput, tx: Prisma.TransactionClient | typeof prisma = prisma) {
    return tx.plan.update({ where: { id }, data, include: { prices: true } });
  }

  async list(args: { skip: number; take: number; isActive?: boolean; q?: string }) {
    const where: Prisma.PlanWhereInput = {
      ...(args.isActive !== undefined ? { isActive: args.isActive } : {}),
      ...(args.q ? { name: { contains: args.q, mode: 'insensitive' } } : {}), 
    };
    const [data, total] = await Promise.all([
      prisma.plan.findMany({ where, skip: args.skip, take: args.take, orderBy: { createdAt: 'desc' }, include: { prices: true } }),
      prisma.plan.count({ where }),
    ]);
    return { data, total };
  }
}

export const plansRepository = new PlansRepository();