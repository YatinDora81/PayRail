import { Router } from 'express';
import { AdminRole } from '@payrail/db';
import { asyncHandler } from '../../lib/asyncHandler';
import { authenticate } from '../../middleware/auth';
import { requireRole } from '../../middleware/rbac';
import { validate } from '../../middleware/validate';
import { promotionsController } from './promotions.controller';
import {
  CreatePromotionSchema,
  UpdatePromotionSchema,
  ListPromotionsQuery,
  PromotionIdParam,
  UpsertPromotionBudgetSchema,
  BudgetsQuery,
  CreatePromotionCouponSchema,
} from './promotions.schema';


export const promotionsRouter : Router = Router();

promotionsRouter.use(authenticate);

promotionsRouter.get('/', requireRole(AdminRole.READONLY), validate({ query: ListPromotionsQuery }), asyncHandler(promotionsController.list));
promotionsRouter.post('/', requireRole(AdminRole.ADMIN), validate({ body: CreatePromotionSchema }), asyncHandler(promotionsController.create));
promotionsRouter.get('/:id', requireRole(AdminRole.READONLY), validate({ params: PromotionIdParam }), asyncHandler(promotionsController.get));
promotionsRouter.patch('/:id', requireRole(AdminRole.ADMIN), validate({ params: PromotionIdParam, body: UpdatePromotionSchema }), asyncHandler(promotionsController.update));

promotionsRouter.post('/:id/activate', requireRole(AdminRole.ADMIN), validate({ params: PromotionIdParam }), asyncHandler(promotionsController.activate));
promotionsRouter.delete('/:id', requireRole(AdminRole.ADMIN), validate({ params: PromotionIdParam }), asyncHandler(promotionsController.deactivate));

promotionsRouter.get('/:id/budgets', requireRole(AdminRole.READONLY), validate({ params: PromotionIdParam, query: BudgetsQuery }), asyncHandler(promotionsController.budgets));
promotionsRouter.put('/:id/budgets', requireRole(AdminRole.ADMIN), validate({ params: PromotionIdParam, body: UpsertPromotionBudgetSchema }), asyncHandler(promotionsController.upsertBudget));

promotionsRouter.get('/:id/coupons', requireRole(AdminRole.READONLY), validate({ params: PromotionIdParam }), asyncHandler(promotionsController.listCoupons));
promotionsRouter.post('/:id/coupons', requireRole(AdminRole.ADMIN), validate({ params: PromotionIdParam, body: CreatePromotionCouponSchema }), asyncHandler(promotionsController.createCoupon));