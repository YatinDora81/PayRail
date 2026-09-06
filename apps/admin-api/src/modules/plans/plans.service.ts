import type { Prisma } from '@payrail/db';
import { prisma } from '../../lib/prisma';
import { plansRepository } from './plans.repository';
import { writeAudit } from '../../lib/audit';
import { paginate, toSkipTake } from '../../lib/pagination';
import { cached, bust } from '../../lib/cache';
import { AppError } from '../../errors';
import type { CreatePlanInput, UpdatePlanInput, ListPlansInput } from './plans.schema';

const itemKey = (id: string): string => `plans:item:${id}`;

const priceRow = (p: CreatePlanInput['prices'][number]) => ({
  country: p.country,
  city: p.city,
  currency: p.currency,
  amountMinor: p.amountMinor, 
  isActive: p.isActive,
});

class PlansService {
  async create(input: CreatePlanInput) {
    return prisma.$transaction(async (tx) => {
      const created = await plansRepository.create(
        {
          name: input.name,
          description: input.description,
          credits: input.credits,
          maxDiscountBps: input.maxDiscountBps,
          sacCode: input.sacCode,
          isActive: input.isActive,
          prices: { create: input.prices.map(priceRow) },
        },
        tx,
      );
      await writeAudit({ action: 'plan.create', entityType: 'Plans', entityId: created.id, after: created }, tx);
      return created;
    });
  }

  async get(id: string) {
    return cached(itemKey(id), 300, async () => {
      const plan = await plansRepository.findById(id);
      if (!plan) throw AppError.notFound('Plan not found');
      return plan;
    });
  }

  async list(query: ListPlansInput) {
    const { skip, take } = toSkipTake(query);
    const sig = `${query.page}:${query.limit}:${query.isActive ?? 'all'}:${query.q ?? ''}`; 
    return cached(`plans:list:${sig}`, 30, async () => {
      const { data, total } = await plansRepository.list({ skip, take, isActive: query.isActive, q: query.q });
      return paginate(data, total, { page: query.page, limit: query.limit });
    });
  }

  async update(id: string, input: UpdatePlanInput) {
    const updated = await prisma.$transaction(async (tx) => {
      const before = await tx.plan.findUnique({ where: { id }, include: { prices: true } });
      if (!before) throw AppError.notFound('Plan not found');

      const data: Prisma.PlanUpdateInput = {
        ...(input.name !== undefined ? { name: input.name } : {}),
        ...(input.description !== undefined ? { description: input.description } : {}),
        ...(input.credits !== undefined ? { credits: input.credits } : {}),
        ...(input.maxDiscountBps !== undefined ? { maxDiscountBps: input.maxDiscountBps } : {}),
        ...(input.sacCode !== undefined ? { sacCode: input.sacCode } : {}),
        ...(input.isActive !== undefined ? { isActive: input.isActive } : {}),
        ...(input.prices
          ? { prices: { deleteMany: {}, create: input.prices.map(priceRow) } }
          : {}),
      };
      const next = await plansRepository.update(id, data, tx);
      await writeAudit({ action: 'plan.update', entityType: 'Plans', entityId: id, before, after: next }, tx);
      return next;
    });

    await bust(itemKey(id)); 
    return updated;
  }

  async deactivate(id: string) {
    const updated = await prisma.$transaction(async (tx) => {
      const before = await tx.plan.findUnique({ where: { id }, include: { prices: true } });
      if (!before) throw AppError.notFound('Plan not found');
      if (!before.isActive) return before; // idempotent
      const next = await plansRepository.update(id, { isActive: false }, tx);
      await writeAudit({ action: 'plan.deactivate', entityType: 'Plans', entityId: id, before, after: next }, tx);
      return next;
    });

    await bust(itemKey(id));
    return updated;
  }
}

export const plansService = new PlansService();