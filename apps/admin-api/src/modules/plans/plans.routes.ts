import { Router } from 'express';
import { AdminRole } from '@payrail/db';
import { asyncHandler } from '../../lib/asyncHandler';
import { authenticate } from '../../middleware/auth';
import { requireRole } from '../../middleware/rbac';
import { validate } from '../../middleware/validate';
import { plansController } from './plans.controller';
import { CreatePlanSchema, UpdatePlanSchema, ListPlansQuery, PlanIdParam } from './plans.schema';

export const plansRouter : Router = Router();
plansRouter.use(authenticate);

plansRouter.get('/', requireRole(AdminRole.READONLY), validate({ query: ListPlansQuery }), asyncHandler(plansController.list));
plansRouter.post('/', requireRole(AdminRole.ADMIN), validate({ body: CreatePlanSchema }), asyncHandler(plansController.create));
plansRouter.get('/:id', requireRole(AdminRole.READONLY), validate({ params: PlanIdParam }), asyncHandler(plansController.get));
plansRouter.patch('/:id', requireRole(AdminRole.ADMIN), validate({ params: PlanIdParam, body: UpdatePlanSchema }), asyncHandler(plansController.update));
plansRouter.delete('/:id', requireRole(AdminRole.ADMIN), validate({ params: PlanIdParam }), asyncHandler(plansController.deactivate));