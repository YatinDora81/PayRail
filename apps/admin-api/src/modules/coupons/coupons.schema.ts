import { z } from 'zod';
import { zDate, zBoolQuery, IdParam } from '../../lib/zod';
import { PaginationQuery } from '../../lib/pagination';

export const CouponFields = z.object({
  code: z.string().min(1).max(64).regex(/^[A-Z0-9._-]+$/, 'Use A-Z, 0-9, dot, underscore or dash'),
  maxRedemptions: z.number().int().positive().nullable().optional(),
  perUserLimit: z.number().int().min(0).default(1),
  startsAt: zDate.optional(),
  endsAt: zDate.optional(),
  isActive: z.boolean().default(true),
});

const windowIsOrdered = (v: { startsAt?: Date; endsAt?: Date }): boolean =>
  !(v.startsAt && v.endsAt) || v.endsAt > v.startsAt;
const windowIssue = { path: ['endsAt'], message: 'endsAt must be after startsAt' };

export const CreateCouponSchema = CouponFields.extend({ promotionId: z.string().min(1) }).refine(windowIsOrdered, windowIssue);

export const UpdateCouponSchema = z
  .object({
    promotionId: z.string().min(1).optional(),
    maxRedemptions: z.number().int().positive().nullable().optional(),
    perUserLimit: z.number().int().min(0).optional(),
    startsAt: zDate.optional(),
    endsAt: zDate.optional(),
    isActive: z.boolean().optional(),
  })
  .refine((v) => Object.keys(v).length > 0, { message: 'No fields to update' })
  .refine(windowIsOrdered, windowIssue);

export const ListCouponsQuery = PaginationQuery.extend({
  isActive: zBoolQuery.optional(),
  promotionId: z.string().min(1).optional(),
  q: z.string().max(200).optional(),
});

export const CouponIdParam = IdParam;

export type CouponFieldsInput = z.infer<typeof CouponFields>;
export type CreateCouponInput = z.infer<typeof CreateCouponSchema>;
export type UpdateCouponInput = z.infer<typeof UpdateCouponSchema>;
export type ListCouponsInput = z.infer<typeof ListCouponsQuery>;