import { z } from 'zod';
import { zCardNetwork, zCardType, zBoolQuery, IdParam } from '../../lib/zod';
import { PaginationQuery } from '../../lib/pagination';

const binDigits = z.string().regex(/^\d{6,8}$/, 'BIN must be 6-8 digits');

export const CreateBinRangeSchema = z
  .object({
    bankName: z.string().min(1).max(120),
    network: zCardNetwork,
    binLow: binDigits,
    binHigh: binDigits,
    cardType: zCardType.optional(),
    isActive: z.boolean().default(true),
  })
  .refine((v) => v.binLow.length === v.binHigh.length, { path: ['binHigh'], message: 'binLow and binHigh must have equal length' })
  .refine((v) => v.binLow <= v.binHigh, { path: ['binHigh'], message: 'binHigh must be >= binLow' });

export const ListBinRangesQuery = PaginationQuery.extend({
  isActive: zBoolQuery.optional(),
  network: zCardNetwork.optional(),
  bankName: z.string().max(120).optional(),
});

export const BinRangeIdParam = IdParam;

export type CreateBinRangeInput = z.infer<typeof CreateBinRangeSchema>;
export type ListBinRangesInput = z.infer<typeof ListBinRangesQuery>;