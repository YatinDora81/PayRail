import type { Prisma } from '@payrail/db';
import { prisma } from '../../lib/prisma';
import { couponsRepository } from './coupons.repository';
import { writeAudit } from '../../lib/audit';
import { paginate, toSkipTake } from '../../lib/pagination';
import { cached, bust } from '../../lib/cache';
import { AppError } from '../../errors';
import type { CouponFieldsInput, CreateCouponInput, UpdateCouponInput, ListCouponsInput } from './coupons.schema';

const itemKey = (id: string): string => `coupons:item:${id}`;
 
export function couponCreateData(c: CouponFieldsInput): Omit<Prisma.CouponCodeCreateInput, 'promotion'> {
  return {
    code: c.code,
    maxRedemptions: c.maxRedemptions ?? null,
    perUserLimit: c.perUserLimit,
    startsAt: c.startsAt,
    endsAt: c.endsAt,
    isActive: c.isActive,
  };
}

export function assertWindow(startsAt: Date | null | undefined, endsAt: Date | null | undefined): void {
  if (startsAt && endsAt && endsAt <= startsAt) throw AppError.badRequest('endsAt must be after startsAt');
}

class CouponsService {
  async create(input: CreateCouponInput) {
    return prisma.$transaction(async (tx) => {
      const promotion = await tx.promotions.findUnique({ where: { id: input.promotionId } });
      if (!promotion) throw AppError.notFound('Promotion not found');
      const created = await couponsRepository.create(
        { ...couponCreateData(input), promotion: { connect: { id: promotion.id } } },
        tx,
      );
      await writeAudit({ action: 'coupon.create', entityType: 'CouponCode', entityId: created.id, after: created }, tx);
      return created;
    });
  }

  async get(id: string) {
    return cached(itemKey(id), 60, async () => {
      const coupon = await couponsRepository.findById(id);
      if (!coupon) throw AppError.notFound('Coupon not found');
      return coupon;
    });
  }

  async list(query: ListCouponsInput) {
    const { skip, take } = toSkipTake(query);
    const sig = `${query.page}:${query.limit}:${query.isActive ?? 'all'}:${query.promotionId ?? ''}:${query.q ?? ''}`;  
    return cached(`coupons:list:${sig}`, 30, async () => {
      const { data, total } = await couponsRepository.list({ skip, take, isActive: query.isActive, promotionId: query.promotionId, q: query.q });
      return paginate(data, total, { page: query.page, limit: query.limit });
    });
  }

  async update(id: string, input: UpdateCouponInput) {
    const updated = await prisma.$transaction(async (tx) => {
      const before = await tx.couponCode.findUnique({ where: { id } });
      if (!before) throw AppError.notFound('Coupon not found');
      const { promotionId, ...fields } = input;
      assertWindow(fields.startsAt ?? before.startsAt, fields.endsAt ?? before.endsAt);
      if (promotionId) {
        const promotion = await tx.promotions.findUnique({ where: { id: promotionId } });
        if (!promotion) throw AppError.notFound('Promotion not found');
      }
      const next = await couponsRepository.update(
        id,
        { ...fields, ...(promotionId ? { promotion: { connect: { id: promotionId } } } : {}) }, 
        tx,
      );
      await writeAudit({ action: 'coupon.update', entityType: 'CouponCode', entityId: id, before, after: next }, tx);
      return next;
    });
    await bust(itemKey(id));
    return updated;
  }

  async deactivate(id: string) {
    const updated = await prisma.$transaction(async (tx) => {
      const before = await tx.couponCode.findUnique({ where: { id } });
      if (!before) throw AppError.notFound('Coupon not found');
      if (!before.isActive) return before;
      const next = await couponsRepository.update(id, { isActive: false }, tx);
      await writeAudit({ action: 'coupon.deactivate', entityType: 'CouponCode', entityId: id, before, after: next }, tx);
      return next;
    });
    await bust(itemKey(id));
    return updated;
  }
}

export const couponsService = new CouponsService();