import { reconciliationRepository } from './reconciliation.repository';
import { paginate, toSkipTake } from '../../lib/pagination';
import { AppError } from '../../errors';
import type { ListReconciliationInput } from './reconciliation.schema';

class ReconciliationService {
  async get(id: string) {
    const entry = await reconciliationRepository.findById(id);
    if (!entry) throw AppError.notFound('Reconciliation entry not found');
    return entry;
  }

  async list(query: ListReconciliationInput) {
    const { skip, take } = toSkipTake(query);
    const { data, total } = await reconciliationRepository.list({
      skip,
      take,
      kind: query.kind,
      promotionId: query.promotionId,
      currency: query.currency,
      corrected: query.corrected,
      deadLetterId: query.deadLetterId,
      from: query.from,
      to: query.to,
    });
    return paginate(data, total, { page: query.page, limit: query.limit });
  }
}

export const reconciliationService = new ReconciliationService();