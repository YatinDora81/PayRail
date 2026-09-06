import { z } from 'zod';
import { zCurrency, zDate, zBoolQuery, IdParam } from '../../lib/zod';
import { PaginationQuery } from '../../lib/pagination';
 
export const ListReconciliationQuery = PaginationQuery.extend({
  kind: z.string().min(1).max(64).optional(),
  promotionId: z.string().min(1).optional(),
  currency: zCurrency.optional(),
  corrected: zBoolQuery.optional(),
  deadLetterId: z.string().min(1).optional(),
  from: zDate.optional(),
  to: zDate.optional(),
});

export const ReconciliationIdParam = IdParam;

export type ListReconciliationInput = z.infer<typeof ListReconciliationQuery>;