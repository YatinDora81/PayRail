import { z } from 'zod';
import { zBoolQuery, IdParam } from '../../lib/zod';
import { PaginationQuery } from '../../lib/pagination';

export const DlqSource = z.enum(['settlement-worker', 'webhook-ingest', 'outbox-relay', 'email-worker']);

export const ListDlqQuery = z.object({
  source: DlqSource.optional(),
  reason: z.string().trim().min(1).max(200).optional(), 
  needsReview: zBoolQuery.optional(),
  cursor: z.string().min(1).optional(),
  limit: PaginationQuery.shape.limit.default(25),
});

export const DlqIdParam = IdParam;

export type ListDlqInput = z.infer<typeof ListDlqQuery>;