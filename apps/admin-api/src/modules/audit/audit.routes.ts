import { Router } from 'express';
import { AdminRole } from '@payrail/db';
import { asyncHandler } from '../../lib/asyncHandler';
import { authenticate } from '../../middleware/auth';
import { requireRole } from '../../middleware/rbac';
import { validate } from '../../middleware/validate';
import { auditController } from './audit.controller';
import { ListAuditQuery, AuditIdParam } from './audit.schema';

export const auditRouter : Router = Router();
auditRouter.use(authenticate);

auditRouter.get('/', requireRole(AdminRole.ADMIN), validate({ query: ListAuditQuery }), asyncHandler(auditController.list));
auditRouter.get('/:id', requireRole(AdminRole.ADMIN), validate({ params: AuditIdParam }), asyncHandler(auditController.get));