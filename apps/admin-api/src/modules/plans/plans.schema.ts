import { z } from 'zod';
import { zMinor, zCurrency, zBps, zCountry, zBoolQuery, IdParam } from '../../lib/zod';
import { PaginationQuery } from '../../lib/pagination';

const PlanPriceInput = z.object({
  country: zCountry,
  city: z.string().max(80).default(''), 
  currency: zCurrency,
  amountMinor: zMinor,
  isActive: z.boolean().default(true),
});
 
const uniqueGeo = (rows: { country: string; city: string }[], ctx: z.RefinementCtx) => {
  const seen = new Set<string>();
  rows.forEach((r, i) => {
    const k = `${r.country}:${r.city || '(default)'}`;
    if (seen.has(k)) ctx.addIssue({ code: 'custom', path: [i], message: `Duplicate price for ${k}` });
    seen.add(k);
  });
};

export const CreatePlanSchema = z.object({
  name: z.string().min(1).max(200),
  description: z.string().max(2000).optional(),
  credits: z.number().int().positive(), 
  maxDiscountBps: zBps.default(10000), 
  sacCode: z.string().max(16).optional(), 
  isActive: z.boolean().default(true),
  prices: z.array(PlanPriceInput).min(1, 'A plan needs at least one price').superRefine(uniqueGeo),
});

export const UpdatePlanSchema = z
  .object({
    name: z.string().min(1).max(200).optional(),
    description: z.string().max(2000).nullable().optional(),
    credits: z.number().int().positive().optional(),
    maxDiscountBps: zBps.optional(),
    sacCode: z.string().max(16).nullable().optional(),
    isActive: z.boolean().optional(),
    prices: z.array(PlanPriceInput).min(1).superRefine(uniqueGeo).optional(),
  })
  .refine((v) => Object.keys(v).length > 0, { message: 'No fields to update' });

export const ListPlansQuery = PaginationQuery.extend({
  isActive: zBoolQuery.optional(),
  q: z.string().max(200).optional(),
});

export const PlanIdParam = IdParam;

export type CreatePlanInput = z.infer<typeof CreatePlanSchema>;
export type UpdatePlanInput = z.infer<typeof UpdatePlanSchema>;
export type ListPlansInput = z.infer<typeof ListPlansQuery>;