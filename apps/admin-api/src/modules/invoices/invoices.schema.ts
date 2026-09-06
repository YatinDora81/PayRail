import { z } from 'zod';
import { zDate, zInvoiceStatus, IdParam } from '../../lib/zod';
import { PaginationQuery } from '../../lib/pagination';
 
export const ListInvoicesQuery = PaginationQuery.extend({
  status: zInvoiceStatus.optional(),
  orderId: z.string().min(1).optional(),
  series: z.string().min(1).max(32).optional(),
  issuedFrom: zDate.optional(),
  issuedTo: zDate.optional(),
});

export const InvoiceIdParam = IdParam;

export type ListInvoicesInput = z.infer<typeof ListInvoicesQuery>;