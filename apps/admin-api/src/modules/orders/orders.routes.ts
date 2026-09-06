import { Router } from 'express';
import { AdminRole } from '@payrail/db';
import { asyncHandler } from '../../lib/asyncHandler';
import { authenticate } from '../../middleware/auth';
import { requireRole } from '../../middleware/rbac';
import { validate } from '../../middleware/validate';
import { ordersController } from './orders.controller';
import { ListOrdersQuery, OrderIdParam } from './orders.schema';

export const ordersRouter : Router = Router();
ordersRouter.use(authenticate);

ordersRouter.get('/', requireRole(AdminRole.SUPPORT), validate({ query: ListOrdersQuery }), asyncHandler(ordersController.list));
ordersRouter.get('/:id', requireRole(AdminRole.SUPPORT), validate({ params: OrderIdParam }), asyncHandler(ordersController.get));
ordersRouter.get('/:id/ledger', requireRole(AdminRole.FINANCE), validate({ params: OrderIdParam }), asyncHandler(ordersController.ledger));
ordersRouter.get('/:id/invoice', requireRole(AdminRole.FINANCE), validate({ params: OrderIdParam }), asyncHandler(ordersController.invoice));