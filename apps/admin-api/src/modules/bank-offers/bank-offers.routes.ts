import { Router } from 'express';
import { AdminRole } from '@payrail/db';
import { asyncHandler } from '../../lib/asyncHandler';
import { authenticate } from '../../middleware/auth';
import { requireRole } from '../../middleware/rbac';
import { validate } from '../../middleware/validate';
import { bankOffersController } from './bank-offers.controller';
import { CreateBankOfferSchema, UpdateBankOfferSchema, ListBankOffersQuery, BankOfferIdParam } from './bank-offers.schema';

export const bankOffersRouter: Router = Router();
bankOffersRouter.use(authenticate);

bankOffersRouter.get('/', requireRole(AdminRole.READONLY), validate({ query: ListBankOffersQuery }), asyncHandler(bankOffersController.list));
bankOffersRouter.post('/', requireRole(AdminRole.ADMIN), validate({ body: CreateBankOfferSchema }), asyncHandler(bankOffersController.create));
bankOffersRouter.get('/:id', requireRole(AdminRole.READONLY), validate({ params: BankOfferIdParam }), asyncHandler(bankOffersController.get));
bankOffersRouter.patch('/:id', requireRole(AdminRole.ADMIN), validate({ params: BankOfferIdParam, body: UpdateBankOfferSchema }), asyncHandler(bankOffersController.update));
bankOffersRouter.delete('/:id', requireRole(AdminRole.ADMIN), validate({ params: BankOfferIdParam }), asyncHandler(bankOffersController.deactivate));