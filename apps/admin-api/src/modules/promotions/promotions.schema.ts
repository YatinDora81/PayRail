import { z } from 'zod';
import { zBps, zMinor, zDate, zCurrency, zStackingMode, zRuleType } from '../../lib/zod';
import { PaginationQuery } from '../../lib/pagination';

// One eligibility rule: a typed ruleType plus free-form params whose shape
// depends on the type (validated structurally, kept as Json on the row).
const RuleInput = z.object({
  ruleType: zRuleType,
  config: z.record(z.string(), z.unknown()).default({}),
});

// An effect, discriminated by effectType so EXACTLY ONE value column is required
// and the wrong combinations are unrepresentable at the edge.
const EffectInput = z.discriminatedUnion('effectType', [
  z.object({ effectType: z.literal('PERCENT_BPS'), valueBps: zBps }),
  z.object({ effectType: z.literal('FLAT_AMOUNT'), amountMinor: zMinor, currency: zCurrency }),
  z.object({ effectType: z.literal('BONUS_CREDITS'), bonusCredits: z.number().int().positive() }),
]);

// One per-currency budget cap (PromotionBudget). Used both inline at create time
// and standalone on PUT /:id/budgets. The reconciler is the sole writer of the
// live Redis counter; admin-api only persists the cap and emits events.
export const UpsertPromotionBudgetSchema = z.object({
  currency: zCurrency,
  capMinor: zMinor,
});

// One coupon (CouponCode). Used inline at create time and standalone on
// POST /:id/coupons. promotionId is never in the body — it comes from context.
export const CreatePromotionCouponSchema = z
  .object({
    code: z
      .string()
      .min(1)
      .max(64)
      .regex(/^[A-Z0-9._-]+$/, 'Use A-Z, 0-9, dot, underscore or dash'),
    maxRedemptions: z.number().int().positive().nullable().optional(),
    perUserLimit: z.number().int().positive().default(1),
    startsAt: zDate.optional(),
    endsAt: zDate.optional(),
    isActive: z.boolean().default(true),
  })
  .refine((v) => !(v.startsAt && v.endsAt) || v.endsAt > v.startsAt, {
    path: ['endsAt'],
    message: 'endsAt must be after startsAt',
  });

// A promotion is ALWAYS created inactive (no isActive here) — flip it on later via
// POST /:id/activate. Budgets and coupons are defined inline at create time:
// budgets are seeded into Redis on activation (not now); coupons attach immediately.
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
    budgets: z
      .array(UpsertPromotionBudgetSchema)
      .default([])
      .superRefine((rows, ctx) => {
        const seen = new Set<string>();
        rows.forEach((b, i) => {
          if (seen.has(b.currency)) {
            ctx.addIssue({ code: 'custom', path: [i, 'currency'], message: `Duplicate budget for ${b.currency}` });
          }
          seen.add(b.currency);
        });
      }),
    coupons: z
      .array(CreatePromotionCouponSchema)
      .default([])
      .superRefine((rows, ctx) => {
        const seen = new Set<string>();
        rows.forEach((c, i) => {
          if (seen.has(c.code)) {
            ctx.addIssue({ code: 'custom', path: [i, 'code'], message: `Duplicate coupon code ${c.code}` });
          }
          seen.add(c.code);
        });
      }),
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
  isActive: z.enum(['true', 'false']).transform((v) => v === 'true').optional(),
  q: z.string().max(200).optional(),
});

export const PromotionIdParam = z.object({ id: z.string().min(1) });

export type CreatePromotionInput = z.infer<typeof CreatePromotionSchema>;
export type UpdatePromotionInput = z.infer<typeof UpdatePromotionSchema>;
export type ListPromotionsInput = z.infer<typeof ListPromotionsQuery>;
export type UpsertPromotionBudgetInput = z.infer<typeof UpsertPromotionBudgetSchema>;
export type CreatePromotionCouponInput = z.infer<typeof CreatePromotionCouponSchema>;