import type { Prisma } from '@payrail/db';
import { prisma } from '../../lib/prisma';
import { dlqRepository } from './dlq.repository';
import { writeAudit } from '../../lib/audit';
import { enqueueOutbox } from '../../lib/outbox';
import { requireContext } from '../../context/requestContext';
import { AppError } from '../../errors';
import type { ListDlqInput } from './dlq.schema';
 
class DlqService {
  async list(query: ListDlqInput) {
    const { page, nextCursor } = await dlqRepository.list(query);
    return { data: page, nextCursor };
  }

  async get(id: string) {
    const row = await dlqRepository.findById(id);
    if (!row) throw AppError.notFound('Dead letter not found');
    return row;
  }

  async replay(id: string) {
    const actor = requireContext().actor;
    if (!actor) throw AppError.unauthorized();

    const row = await dlqRepository.findById(id);
    if (!row) throw AppError.notFound('Dead letter not found');
    if (row.replayedAt) {
      throw AppError.conflict(`Already replayed at ${row.replayedAt.toISOString()}`, 'ALREADY_REPLAYED');
    }

    const won = await prisma.$transaction(async (tx) => {
      if (!(await dlqRepository.claimReplay(row.id, actor.id, tx))) return false; 
      await enqueueOutbox(tx, row.topic, row.key, row.payload as Prisma.InputJsonObject);
      await writeAudit(
        { action: 'dlq.replay', entityType: 'DeadLetterEvent', entityId: row.id, after: { topic: row.topic, source: row.source, reason: row.reason } },
        tx, 
      );
      return true;
    });
    if (!won) throw AppError.conflict('A concurrent replay already won', 'ALREADY_REPLAYED');
    return { replayed: true, topic: row.topic };
  }
}

export const dlqService = new DlqService();