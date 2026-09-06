import { Router } from 'express';
import { AdminRole } from '@payrail/db';
import { asyncHandler } from '../../lib/asyncHandler';
import { authenticate } from '../../middleware/auth';
import { requireRole } from '../../middleware/rbac';
import { validate } from '../../middleware/validate';
import { dlqController } from './dlq.controller';
import { ListDlqQuery, DlqIdParam } from './dlq.schema';

export const dlqRouter : Router = Router();
dlqRouter.use(authenticate, requireRole(AdminRole.ADMIN));

dlqRouter.get('/', validate({ query: ListDlqQuery }), asyncHandler(dlqController.list));
dlqRouter.get('/:id', validate({ params: DlqIdParam }), asyncHandler(dlqController.get));
dlqRouter.post('/:id/replay', validate({ params: DlqIdParam }), asyncHandler(dlqController.replay));