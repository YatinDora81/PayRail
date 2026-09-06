import { Router } from 'express';
import { AdminRole } from '@payrail/db';
import { asyncHandler } from '../../lib/asyncHandler';
import { authenticate } from '../../middleware/auth';
import { requireRole } from '../../middleware/rbac';
import { validate } from '../../middleware/validate';
import { invoicesController } from './invoices.controller';
import { ListInvoicesQuery, InvoiceIdParam } from './invoices.schema';

export const invoicesRouter : Router = Router();
invoicesRouter.use(authenticate);

invoicesRouter.get('/', requireRole(AdminRole.FINANCE), validate({ query: ListInvoicesQuery }), asyncHandler(invoicesController.list));
invoicesRouter.get('/:id', requireRole(AdminRole.FINANCE), validate({ params: InvoiceIdParam }), asyncHandler(invoicesController.get));
invoicesRouter.get('/:id/pdf', requireRole(AdminRole.FINANCE), validate({ params: InvoiceIdParam }), asyncHandler(invoicesController.pdf));