import { DisputeStatus, type Prisma } from '@payrail/db';
import { prisma } from '../../lib/prisma';
import { disputesRepository } from './disputes.repository';
import { writeAudit } from '../../lib/audit';
import { paginate, toSkipTake } from '../../lib/pagination';
import { AppError } from '../../errors';
import type { SubmitEvidenceInput, AcceptDisputeInput, ListDisputesInput } from './disputes.schema';

class DisputesService {
  async get(id: string) {
    const dispute = await disputesRepository.findById(id);
    if (!dispute) throw AppError.notFound('Dispute not found');
    return dispute;
  }

  async list(query: ListDisputesInput) {
    const { skip, take } = toSkipTake(query);
    const { data, total } = await disputesRepository.list({ skip, take, status: query.status, paymentId: query.paymentId, orderId: query.orderId });
    return paginate(data, total, { page: query.page, limit: query.limit });
  }
 
  async submitEvidence(id: string, input: SubmitEvidenceInput) {
    return prisma.$transaction(async (tx) => {
      const before = await tx.dispute.findUnique({ where: { id } });
      if (!before) throw AppError.notFound('Dispute not found');
      if (before.status !== DisputeStatus.NEEDS_RESPONSE) {
        throw AppError.conflict(`Dispute is ${before.status}; evidence can only be submitted while NEEDS_RESPONSE`, 'DISPUTE_NOT_CONTESTABLE');
      }
      const next = await disputesRepository.update(
        id,
        {
          status: DisputeStatus.UNDER_REVIEW,
          evidence: input.evidence as Prisma.InputJsonObject,
          ...(input.note !== undefined ? { note: input.note } : {}),
        },
        tx,
      );
      await writeAudit({ action: 'dispute.submitEvidence', entityType: 'Dispute', entityId: id, before, after: next }, tx);
      return next;
    });
  }
 
  async accept(id: string, input: AcceptDisputeInput) {
    return prisma.$transaction(async (tx) => {
      const before = await tx.dispute.findUnique({ where: { id } });
      if (!before) throw AppError.notFound('Dispute not found');
      if (before.status === DisputeStatus.ACCEPTED) return before;
      if (before.status === DisputeStatus.WON || before.status === DisputeStatus.LOST) {
        throw AppError.conflict(`Dispute already resolved (${before.status})`, 'DISPUTE_RESOLVED');
      }
      const next = await disputesRepository.update(
        id,
        { status: DisputeStatus.ACCEPTED, resolvedAt: new Date(), ...(input.note !== undefined ? { note: input.note } : {}) },
        tx,
      );
      await writeAudit({ action: 'dispute.accept', entityType: 'Dispute', entityId: id, before, after: next }, tx);
      return next;
    });
  }
}

export const disputesService = new DisputesService();