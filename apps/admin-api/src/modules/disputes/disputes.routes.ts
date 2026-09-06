import { Router } from 'express';
import { AdminRole } from '@payrail/db';
import { asyncHandler } from '../../lib/asyncHandler';
import { authenticate } from '../../middleware/auth';
import { requireRole } from '../../middleware/rbac';
import { validate } from '../../middleware/validate';
import { disputesController } from './disputes.controller';
import { SubmitEvidenceSchema, AcceptDisputeSchema, ListDisputesQuery, DisputeIdParam } from './disputes.schema';

export const disputesRouter : Router = Router();
disputesRouter.use(authenticate);

disputesRouter.get('/', requireRole(AdminRole.FINANCE), validate({ query: ListDisputesQuery }), asyncHandler(disputesController.list));
disputesRouter.get('/:id', requireRole(AdminRole.FINANCE), validate({ params: DisputeIdParam }), asyncHandler(disputesController.get));
disputesRouter.patch('/:id/evidence', requireRole(AdminRole.FINANCE), validate({ params: DisputeIdParam, body: SubmitEvidenceSchema }), asyncHandler(disputesController.submitEvidence));
disputesRouter.patch('/:id/accept', requireRole(AdminRole.FINANCE), validate({ params: DisputeIdParam, body: AcceptDisputeSchema }), asyncHandler(disputesController.accept));