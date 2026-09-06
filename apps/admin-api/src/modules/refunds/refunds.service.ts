import { Prisma, RefundStatus } from '@payrail/db';
import { prisma } from '../../lib/prisma';
import { refundsRepository } from './refunds.repository';
import { createRefund as gatewayCreateRefund, GatewayRefundRejectedError } from '../../clients/gateway.client';
import { writeAudit } from '../../lib/audit';
import { requireContext } from '../../context/requestContext';
import { AppError } from '../../errors';
import type { CreateRefundInput } from './refunds.schema';

class RefundsService {
  async create(input: CreateRefundInput, idempotencyKey: string) {
    const { refund, replayed, gatewayPaymentId, gatewayOrderId } = await prisma.$transaction(async (tx) => {
 
      await tx.$queryRaw(Prisma.sql`SELECT "id" FROM "Payment" WHERE "id" = ${input.paymentId} FOR UPDATE`);

      const payment = await refundsRepository.findPaymentWithRefunds(input.paymentId, tx);
      if (!payment) throw AppError.notFound('Payment not found');
      if (!payment.gatewayPaymentId) throw AppError.badRequest('Payment has no gateway payment id; cannot refund');
 
      const order = await tx.order.findUnique({
        where: { id: payment.orderId },
        select: { status: true, gatewayOrderId: true },  
      });
      if (!order) throw AppError.notFound('Order not found');
      if (order.status === 'DISPUTED') {
        throw AppError.conflict('Order is under dispute; resolve the chargeback before refunding', 'ORDER_NOT_REFUNDABLE');
      }
      if (order.status === 'REFUNDED') throw AppError.conflict('Order is already fully refunded', 'ORDER_NOT_REFUNDABLE');
 
      const existing = await refundsRepository.findByIdempotencyKey(idempotencyKey, tx);
      if (existing) {
        if (existing.paymentId !== payment.id || existing.amountMinor !== input.amountMinor) {
          throw AppError.conflict('Idempotency-Key already used for a different refund', 'IDEMPOTENCY_CONFLICT');
        }
        return { refund: existing, replayed: true, gatewayPaymentId: payment.gatewayPaymentId, gatewayOrderId: order.gatewayOrderId ?? '' };
      }

      const alreadyRefunded = payment.refunds
        .filter((r) => r.status !== RefundStatus.FAILED)
        .reduce((sum, r) => sum + r.amountMinor, 0n); 
      const refundable = payment.amountMinor - alreadyRefunded; 
      if (input.amountMinor > refundable) {
        throw AppError.badRequest(`Refund exceeds refundable amount (${refundable.toString()} minor units remaining)`);
      }

      const created = await refundsRepository.create(
        {
          payment: { connect: { id: payment.id } },
          order: { connect: { id: payment.orderId } },
          gateway: payment.gateway,
          amountMinor: input.amountMinor,
          currency: payment.currency,
          status: RefundStatus.PENDING,
          reason: input.reason,
          idempotencyKey,
        },
        tx,
      );
      return { refund: created, replayed: false, gatewayPaymentId: payment.gatewayPaymentId, gatewayOrderId: order.gatewayOrderId ?? '' };
    });
    if (replayed) return refund; 
 
    let result;
    try {
      result = await gatewayCreateRefund({
        gateway: refund.gateway,
        gatewayPaymentId,
        gatewayOrderId,
        amountMinor: input.amountMinor,
        currency: refund.currency,
        idempotencyKey,
      });
    } catch (err) {
      if (err instanceof GatewayRefundRejectedError) {
 
        await prisma.refund.update({ where: { id: refund.id }, data: { status: RefundStatus.FAILED } }).catch(() => undefined);
        requireContext().logger.error({ err, refundId: refund.id }, 'gateway rejected refund');
        throw new AppError(err.statusCode === 409 ? 409 : 400, 'REFUND_REJECTED', `Provider rejected refund: ${err.message}`);
      }
 
      requireContext().logger.error({ err, refundId: refund.id }, 'gateway refund outcome unknown; left PENDING for gateway-reconciler');
      throw AppError.upstream('Refund outcome unknown; it stays PENDING and will be reconciled');
    }
 
    try {
      return await prisma.$transaction(async (tx) => {
        const updated = await tx.refund.update({ where: { id: refund.id }, data: { gatewayRefundId: result.gatewayRefundId } });
        await writeAudit({ action: 'refund.issue', entityType: 'Refund', entityId: updated.id, after: updated }, tx);
        return updated;
      });
    } catch (err) {
      requireContext().logger.error({ err, refundId: refund.id }, 'refund executed at provider but local update failed; left PENDING for gateway-reconciler');
      throw err;
    }
  }

  async get(id: string) {
    const refund = await refundsRepository.findById(id);
    if (!refund) throw AppError.notFound('Refund not found');
    return refund;
  }
}

export const refundsService = new RefundsService();