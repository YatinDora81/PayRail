import { z } from 'zod';
import { zOrderStatus, zGateway, zDate, IdParam } from '../../lib/zod';
import { PaginationQuery } from '../../lib/pagination';

export const ListOrdersQuery = PaginationQuery.extend({
  status: zOrderStatus.optional(),
  userId: z.string().min(1).optional(),
  gateway: zGateway.optional(),
  gatewayOrderId: z.string().min(1).optional(),
  from: zDate.optional(),
  to: zDate.optional(),
});

export const OrderIdParam = IdParam;

export type ListOrdersInput = z.infer<typeof ListOrdersQuery>;