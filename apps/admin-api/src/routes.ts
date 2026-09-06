import { Router } from "express";

const router: Router = Router();

export const adminRouter : Router = Router();

// // catalog + promotions (ADMIN writes)
// adminRouter.use('/plans', plansRouter);
// adminRouter.use('/promotions', promotionsRouter);
// adminRouter.use('/coupons', couponsRouter);
// adminRouter.use('/bank-offers', bankOffersRouter);
// adminRouter.use('/bin-ranges', binRangesRouter);
// // money (FINANCE)
// adminRouter.use('/orders', ordersRouter);
// adminRouter.use('/refunds', refundsRouter);
// adminRouter.use('/disputes', disputesRouter);
// adminRouter.use('/invoices', invoicesRouter);
// adminRouter.use('/reconciliation', reconciliationRouter);
// // operations (ADMIN)
// adminRouter.use('/audit', auditRouter);
// adminRouter.use('/dlq', dlqRouter);