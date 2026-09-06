import { z } from 'zod';
import { zBps, zMinor, zDate, zCurrency, zStackingMode, zBoolQuery, IdParam } from '../../lib/zod';
import { PaginationQuery } from '../../lib/pagination';
import { CouponFields } from '../coupons/coupons.schema';


const RuleInput = z.discriminatedUnion('ruleType', [
  z.object({ ruleType: z.literal('PLAN_IN'), config: z.object({ planIds: z.array(z.string().min(1)).min(1) }) }),
  z.object({ ruleType: z.literal('MIN_AMOUNT_MINOR'), config: z.object({ currency: zCurrency, amountMinor: z.number().int().nonnegative() }) }),
  z.object({ ruleType: z.literal('COUNTRY_IN'), config: z.object({ countries: z.array(z.string().length(2).toUpperCase()).min(1) }) }),
  z.object({ ruleType: z.literal('FIRST_PURCHASE_ONLY'), config: z.object({}).strict().default({}) }),
]);


const EffectInput = z.discriminatedUnion('effectType', [
  z.object({ effectType: z.literal('PERCENT_BPS'), valueBps: zBps }),
  z.object({ effectType: z.literal('FLAT_AMOUNT'), amountMinor: zMinor, currency: zCurrency }),
  z.object({ effectType: z.literal('BONUS_CREDITS'), bonusCredits: z.number().int().positive() }),
]);

export const UpsertPromotionBudgetSchema = z.object({
  currency: zCurrency,
  capMinor: zMinor,
});


export const BudgetsQuery = z.object({ currency: zCurrency.optional() });

export const CreatePromotionCouponSchema = CouponFields.refine(
  (v : any) => !(v.startsAt && v.endsAt) || v.endsAt > v.startsAt,
  { path: ['endsAt'], message: 'endsAt must be after startsAt' },
);

const noDuplicates = <T>(pick: (row: T) => string, label: string) => (rows: T[], ctx: z.RefinementCtx) => {
  const seen = new Set<string>(); 
  rows.forEach((row, i) => {
    const key = pick(row);
    if (seen.has(key)) ctx.addIssue({ code: 'custom', path: [i], message: `Duplicate ${label} ${key}` });
    seen.add(key);
  });
};

export const CreatePromotionSchema = z
  .object({
    name: z.string().min(1).max(200),
    description: z.string().max(2000).optional(),
    stackingMode: zStackingMode.default('EXCLUSIVE'),
    priority: z.number().int().default(0),
    startsAt: zDate,
    endsAt: zDate,
    rules: z.array(RuleInput).default([]),
    effects: z.array(EffectInput).min(1, 'At least one effect is required'),
    budgets: z.array(UpsertPromotionBudgetSchema).default([]).superRefine(noDuplicates((b) => b.currency, 'budget for')),
    coupons: z.array(CreatePromotionCouponSchema).default([]).superRefine(noDuplicates((c) => c.code, 'coupon code')),
  })
  .refine((v) => v.endsAt > v.startsAt, { path: ['endsAt'], message: 'endsAt must be after startsAt' });

export const UpdatePromotionSchema = z
  .object({
    name: z.string().min(1).max(200).optional(),
    description: z.string().max(2000).nullable().optional(),
    stackingMode: zStackingMode.optional(),
    priority: z.number().int().optional(),
    startsAt: zDate.optional(),
    endsAt: zDate.optional(),
  })
  .refine((v) => Object.keys(v).length > 0, { message: 'No fields to update' });

export const ListPromotionsQuery = PaginationQuery.extend({
  isActive: zBoolQuery.optional(),
  q: z.string().max(200).optional(),
});

export const PromotionIdParam = IdParam;

export type CreatePromotionInput = z.infer<typeof CreatePromotionSchema>;
export type UpdatePromotionInput = z.infer<typeof UpdatePromotionSchema>;
export type ListPromotionsInput = z.infer<typeof ListPromotionsQuery>;
export type UpsertPromotionBudgetInput = z.infer<typeof UpsertPromotionBudgetSchema>;
export type BudgetsQueryInput = z.infer<typeof BudgetsQuery>;
export type CreatePromotionCouponInput = z.infer<typeof CreatePromotionCouponSchema>;