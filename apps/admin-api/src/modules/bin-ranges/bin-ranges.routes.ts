import { Router } from 'express';
import { AdminRole } from '@payrail/db';
import { asyncHandler } from '../../lib/asyncHandler';
import { authenticate } from '../../middleware/auth';
import { requireRole } from '../../middleware/rbac';
import { validate } from '../../middleware/validate';
import { binRangesController } from './bin-ranges.controller';
import { CreateBinRangeSchema, ListBinRangesQuery, BinRangeIdParam } from './bin-ranges.schema';

export const binRangesRouter : Router = Router();
binRangesRouter.use(authenticate);

binRangesRouter.get('/', requireRole(AdminRole.READONLY), validate({ query: ListBinRangesQuery }), asyncHandler(binRangesController.list));
binRangesRouter.post('/', requireRole(AdminRole.ADMIN), validate({ body: CreateBinRangeSchema }), asyncHandler(binRangesController.create));
binRangesRouter.get('/:id', requireRole(AdminRole.READONLY), validate({ params: BinRangeIdParam }), asyncHandler(binRangesController.get));