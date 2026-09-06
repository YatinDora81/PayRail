import { Router } from 'express';
import { AdminRole } from '@payrail/db';
import { asyncHandler } from '../../lib/asyncHandler';
import { authenticate } from '../../middleware/auth';
import { requireRole } from '../../middleware/rbac';
import { validate } from '../../middleware/validate';
import { reconciliationController } from './reconciliation.controller';
import { ListReconciliationQuery, ReconciliationIdParam } from './reconciliation.schema';

export const reconciliationRouter : Router = Router();
reconciliationRouter.use(authenticate);

reconciliationRouter.get('/', requireRole(AdminRole.FINANCE), validate({ query: ListReconciliationQuery }), asyncHandler(reconciliationController.list));
reconciliationRouter.get('/:id', requireRole(AdminRole.FINANCE), validate({ params: ReconciliationIdParam }), asyncHandler(reconciliationController.get));