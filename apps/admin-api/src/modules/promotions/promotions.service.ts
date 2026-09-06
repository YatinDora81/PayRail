import type { Prisma, Currency } from '@payrail/db';
import { prisma } from '../../lib/prisma';
import { redis } from '../../clients/redis.client';
import { promotionsRepository } from './promotions.repository';
import { couponsRepository } from '../coupons/coupons.repository';
import { couponCreateData, assertWindow } from '../coupons/coupons.service';
import { writeAudit } from '../../lib/audit';
import { enqueueOutbox } from '../../lib/outbox';
import { paginate, toSkipTake } from '../../lib/pagination';
import { cached, bust } from '../../lib/cache';
import { AppError } from '../../errors';
import type {
  CreatePromotionInput,
  UpdatePromotionInput,
  ListPromotionsInput,
  UpsertPromotionBudgetInput,
  CreatePromotionCouponInput,
} from './promotions.schema';

const TOPIC_BUDGET_UPSERTED = 'promotion.budget.upserted';
const TOPIC_ACTIVATED = 'promotion.activated';

const itemKey = (id: string): string => `promotions:item:${id}`;

const adminListKey = 'promotions:admin:list';


const budgetCounterKey = (promotionId: string, currency: string): string => `promo:budget:${promotionId}:${currency}`;


function buildEffectsCreate(effects: CreatePromotionInput['effects']): Prisma.PromotionEffectsCreateWithoutPromotionInput[] {
  return effects.map((e) => {
    switch (e.effectType) {
      case 'PERCENT_BPS':
        return { effectType: 'PERCENT_BPS', valueBps: e.valueBps };
      case 'FLAT_AMOUNT':
        return { effectType: 'FLAT_AMOUNT', amountMinor: e.amountMinor, currency: e.currency };
      case 'BONUS_CREDITS':
        return { effectType: 'BONUS_CREDITS', bonusCredits: e.bonusCredits };
    }
  });
}
function currenciesIn(input: CreatePromotionInput): Currency[] {
  const fromBudgets = input.budgets.map((b) => b.currency);
  const fromEffects = input.effects.flatMap((e) => (e.effectType === 'FLAT_AMOUNT' ? [e.currency] : []));
  return [...new Set([...fromBudgets, ...fromEffects])];
}

async function assertCurrenciesSupported(currencies: Currency[]): Promise<void> {
  const supported = new Set(await promotionsRepository.supportedCurrencies());
  const bad = currencies.filter((c) => !supported.has(c));
  if (bad.length) throw AppError.badRequest(`Unsupported or disabled currency: ${bad.join(', ')}`);
}

async function readCounter(promotionId: string, currency: string): Promise<bigint | null> {
  try {
    const raw = await redis.get(budgetCounterKey(promotionId, currency));
    return raw !== null && /^-?\d+$/.test(raw) ? BigInt(raw) : null;
  } catch {
    return null;
  }
}

class PromotionsService {
  async create(input: CreatePromotionInput) {
    
    await assertCurrenciesSupported(currenciesIn(input));

    const created = await prisma.$transaction(async (tx) => {
      const promo = await promotionsRepository.create(
        {
          name: input.name,
          description: input.description,
          stackingMode: input.stackingMode,
          priority: input.priority,
          startsAt: input.startsAt,
          endsAt: input.endsAt,
          isActive: false,
          rules: { create: input.rules.map((r) => ({ ruleType: r.ruleType, config: r.config as Prisma.InputJsonValue })) },
          effects: { create: buildEffectsCreate(input.effects) },
          budgets: { create: input.budgets.map((b) => ({ currency: b.currency, capMinor: b.capMinor })) },
          coupons: { create: input.coupons.map(couponCreateData) },
        },
        tx,
      );
      await writeAudit({ action: 'promotion.create', entityType: 'Promotions', entityId: promo.id, after: promo }, tx);
      return promo;
    });

    await bust(adminListKey); 
    return created;
  }

  async get(id: string) {
    return cached(itemKey(id), 300, async () => {
      const promotion = await promotionsRepository.findById(id);
      if (!promotion) throw AppError.notFound('Promotion not found');
      return promotion;
    });
  }
 
  async list(query: ListPromotionsInput) {
    const all = await cached(adminListKey, 300, () => promotionsRepository.listAll());
    const q = query.q?.toLowerCase();
    const filtered = all.filter(
      (p) => (query.isActive === undefined || p.isActive === query.isActive) && (!q || p.name.toLowerCase().includes(q)),
    );
    const { skip, take } = toSkipTake(query);
    return paginate(filtered.slice(skip, skip + take), filtered.length, { page: query.page, limit: query.limit });
  }

  async update(id: string, input: UpdatePromotionInput) {
    const updated = await prisma.$transaction(async (tx) => {
      const before = await tx.promotions.findUnique({ where: { id } });
      if (!before) throw AppError.notFound('Promotion not found');
      assertWindow(input.startsAt ?? before.startsAt, input.endsAt ?? before.endsAt); 
      const next = await promotionsRepository.update(id, { ...input }, tx);
      await writeAudit({ action: 'promotion.update', entityType: 'Promotions', entityId: id, before, after: next }, tx);
      return next;
    });

    await bust(itemKey(id), adminListKey); 
    return updated;
  }
 
  async activate(id: string) {
    const promotion = await prisma.$transaction(async (tx) => {
      const before = await tx.promotions.findUnique({ where: { id } });
      if (!before) throw AppError.notFound('Promotion not found');
      if (before.isActive) return before; 
      const next = await promotionsRepository.update(id, { isActive: true }, tx);
      await writeAudit({ action: 'promotion.activate', entityType: 'Promotions', entityId: id, before, after: next }, tx);
      await enqueueOutbox(tx, TOPIC_ACTIVATED, id, { promotionId: id });
      return next;
    });

    await bust(itemKey(id), adminListKey);
    return promotion;
  }

  async deactivate(id: string) {
    const updated = await prisma.$transaction(async (tx) => {
      const before = await tx.promotions.findUnique({ where: { id } });
      if (!before) throw AppError.notFound('Promotion not found');
      if (!before.isActive) return before;
      const next = await promotionsRepository.update(id, { isActive: false }, tx);
      await writeAudit({ action: 'promotion.deactivate', entityType: 'Promotions', entityId: id, before, after: next }, tx);
      return next;
    });

    await bust(itemKey(id), adminListKey);
    return updated;
  } 

  async upsertBudget(id: string, input: UpsertPromotionBudgetInput) {
    await assertCurrenciesSupported([input.currency]);
    const budget = await prisma.$transaction(async (tx) => {
      const promotion = await tx.promotions.findUnique({ where: { id } });
      if (!promotion) throw AppError.notFound('Promotion not found');
      const next = await promotionsRepository.upsertBudget(id, input.currency, input.capMinor, tx);
      await writeAudit({ action: 'promotion.budget.upsert', entityType: 'PromotionBudget', entityId: next.id, after: next }, tx);
      if (promotion.isActive) {
        await enqueueOutbox(tx, TOPIC_BUDGET_UPSERTED, id, {
          promotionId: id,
          currency: input.currency,
          capMinor: input.capMinor.toString(), 
        });
      }
      return next;
    });

    await bust(itemKey(id));
    return budget;
  }

  async budgets(id: string, currency?: Currency) {
    const promotion = await prisma.promotions.findUnique({ where: { id }, select: { id: true } });
    if (!promotion) throw AppError.notFound('Promotion not found');
    const [rows, spent] = await Promise.all([promotionsRepository.listBudgets(id, currency), promotionsRepository.spentByCurrency(id)]);
    if (currency && rows.length === 0) throw AppError.notFound(`Promotion has no ${currency} budget`);
    const views = await Promise.all(
      rows.map(async (b) => {
        const remainingMinor = await readCounter(id, b.currency);
        return {
          ...b,
          spentMinor: spent.get(b.currency) ?? 0n,
          remainingMinor,
          seeded: remainingMinor !== null,
        };
      }),
    );
    return currency ? views[0] : views;
  }

  async createCoupon(id: string, input: CreatePromotionCouponInput) {
    const coupon = await prisma.$transaction(async (tx) => {
      const promotion = await tx.promotions.findUnique({ where: { id } });
      if (!promotion) throw AppError.notFound('Promotion not found');
      const created = await promotionsRepository.createCoupon(id, couponCreateData(input), tx);
      await writeAudit({ action: 'coupon.create', entityType: 'CouponCode', entityId: created.id, after: created }, tx);
      return created;
    });

    await bust(itemKey(id));
    return coupon;
  }

  listCoupons(id: string) {
    return couponsRepository.listByPromotion(id);
  }
}

export const promotionsService = new PromotionsService();