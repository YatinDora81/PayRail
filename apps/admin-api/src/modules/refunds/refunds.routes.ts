import { Router } from 'express';
import { AdminRole } from '@payrail/db';
import { asyncHandler } from '../../lib/asyncHandler';
import { authenticate } from '../../middleware/auth';
import { requireRole } from '../../middleware/rbac';
import { validate } from '../../middleware/validate';
import { idempotency } from '../../middleware/idempotency';
import { refundsController } from './refunds.controller';
import { CreateRefundSchema, RefundIdParam } from './refunds.schema';

export const refundsRouter : Router= Router();

refundsRouter.use(authenticate);

refundsRouter.post(
  '/',
  requireRole(AdminRole.FINANCE),
  validate({ body: CreateRefundSchema }),
  idempotency('POST /v1/admin/refunds'),
  asyncHandler(refundsController.create),
);

refundsRouter.get(
  '/:id',
  requireRole(AdminRole.FINANCE),
  validate({ params: RefundIdParam }),
  asyncHandler(refundsController.get),
);