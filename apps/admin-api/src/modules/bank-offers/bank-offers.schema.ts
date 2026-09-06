import { z } from 'zod';
import { zMinor, zCurrency, zBps, zDate, zCountry, zCardNetwork, zBankOfferType, zBankOfferFunding, zBoolQuery, IdParam } from '../../lib/zod';
import { PaginationQuery } from '../../lib/pagination';
 
export const CreateBankOfferSchema = z
  .object({
    bankName: z.string().min(1).max(120),
    cardNetwork: zCardNetwork.optional(),
    binRangeId: z.string().min(1).optional(),
    description: z.string().max(500).optional(),
    country: zCountry.or(z.literal('')).default(''), // "" = every market
    type: zBankOfferType.default('INSTANT_DISCOUNT'),
    funding: zBankOfferFunding.default('BANK'),
    discountBps: zBps,
    maxDiscountMinor: zMinor.optional(),
    minAmountMinor: zMinor.default(0n),
    currency: zCurrency,
    startsAt: zDate,
    endsAt: zDate,
    isActive: z.boolean().default(true),
  })
  .refine((v) => v.endsAt > v.startsAt, { path: ['endsAt'], message: 'endsAt must be after startsAt' });

export const UpdateBankOfferSchema = z
  .object({
    bankName: z.string().min(1).max(120).optional(),
    cardNetwork: zCardNetwork.nullable().optional(),
    binRangeId: z.string().min(1).nullable().optional(),  
    description: z.string().max(500).nullable().optional(),
    country: zCountry.or(z.literal('')).optional(),
    type: zBankOfferType.optional(),
    funding: zBankOfferFunding.optional(),
    discountBps: zBps.optional(),
    maxDiscountMinor: zMinor.nullable().optional(),
    minAmountMinor: zMinor.optional(),
    startsAt: zDate.optional(),
    endsAt: zDate.optional(),
    isActive: z.boolean().optional(),
  })
  .refine((v) => Object.keys(v).length > 0, { message: 'No fields to update' });

export const ListBankOffersQuery = PaginationQuery.extend({
  isActive: zBoolQuery.optional(),
  bankName: z.string().max(120).optional(),
  country: zCountry.optional(),
});

export const BankOfferIdParam = IdParam;

export type CreateBankOfferInput = z.infer<typeof CreateBankOfferSchema>;
export type UpdateBankOfferInput = z.infer<typeof UpdateBankOfferSchema>;
export type ListBankOffersInput = z.infer<typeof ListBankOffersQuery>;