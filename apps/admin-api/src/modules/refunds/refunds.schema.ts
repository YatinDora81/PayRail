import { z } from 'zod';
import { zMinor, IdParam } from '../../lib/zod';

export const CreateRefundSchema = z.object({
  paymentId: z.string().min(1),
  amountMinor: zMinor.refine((v) => v > 0n, 'Refund amount must be positive'),
  reason: z.string().max(500).optional(),
});

export const RefundIdParam = IdParam;

export type CreateRefundInput = z.infer<typeof CreateRefundSchema>;