import { z } from 'zod';
import { zDate, IdParam } from '../../lib/zod';
import { PaginationQuery } from '../../lib/pagination';

export const ListAuditQuery = PaginationQuery.extend({
  actorId: z.string().min(1).optional(),
  action: z.string().min(1).max(64).optional(),     // e.g. "promotion.create"
  entityType: z.string().min(1).max(64).optional(), // e.g. "Promotions"
  entityId: z.string().min(1).optional(),
  from: zDate.optional(),
  to: zDate.optional(),
});

export const AuditIdParam = IdParam;

export type ListAuditInput = z.infer<typeof ListAuditQuery>;