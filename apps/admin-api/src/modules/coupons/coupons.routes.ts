import { Router } from 'express';
import { AdminRole } from '@payrail/db';
import { asyncHandler } from '../../lib/asyncHandler';
import { authenticate } from '../../middleware/auth';
import { requireRole } from '../../middleware/rbac';
import { validate } from '../../middleware/validate';
import { couponsController } from './coupons.controller';
import { CreateCouponSchema, UpdateCouponSchema, ListCouponsQuery, CouponIdParam } from './coupons.schema';

export const couponsRouter : Router = Router();
couponsRouter.use(authenticate);

couponsRouter.get('/', requireRole(AdminRole.READONLY), validate({ query: ListCouponsQuery }), asyncHandler(couponsController.list));
couponsRouter.post('/', requireRole(AdminRole.ADMIN), validate({ body: CreateCouponSchema }), asyncHandler(couponsController.create));
couponsRouter.get('/:id', requireRole(AdminRole.READONLY), validate({ params: CouponIdParam }), asyncHandler(couponsController.get));
couponsRouter.patch('/:id', requireRole(AdminRole.ADMIN), validate({ params: CouponIdParam, body: UpdateCouponSchema }), asyncHandler(couponsController.update));
couponsRouter.delete('/:id', requireRole(AdminRole.ADMIN), validate({ params: CouponIdParam }), asyncHandler(couponsController.deactivate));