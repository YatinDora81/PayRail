import { z } from 'zod';
import { zDisputeStatus, IdParam } from '../../lib/zod';
import { PaginationQuery } from '../../lib/pagination';
 
export const SubmitEvidenceSchema = z.object({
  evidence: z.record(z.string(), z.unknown()).refine((o) => Object.keys(o).length > 0, 'Evidence cannot be empty'),
  note: z.string().max(1000).optional(),
});

export const AcceptDisputeSchema = z.object({
  note: z.string().max(1000).optional(),
});

export const ListDisputesQuery = PaginationQuery.extend({
  status: zDisputeStatus.optional(),
  paymentId: z.string().min(1).optional(),
  orderId: z.string().min(1).optional(),
});

export const DisputeIdParam = IdParam;

export type SubmitEvidenceInput = z.infer<typeof SubmitEvidenceSchema>;
export type AcceptDisputeInput = z.infer<typeof AcceptDisputeSchema>;
export type ListDisputesInput = z.infer<typeof ListDisputesQuery>;