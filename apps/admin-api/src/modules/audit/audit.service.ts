import { auditRepository } from './audit.repository';
import { paginate, toSkipTake } from '../../lib/pagination';
import { AppError } from '../../errors';
import type { ListAuditInput } from './audit.schema';

class AuditService {
  async get(id: string) {
    const entry = await auditRepository.findById(id);
    if (!entry) throw AppError.notFound('Audit entry not found');
    return entry;
  }

  async list(query: ListAuditInput) {
    const { skip, take } = toSkipTake(query);
    const { data, total } = await auditRepository.list({
      skip,
      take,
      actorId: query.actorId,
      action: query.action,
      entityType: query.entityType,
      entityId: query.entityId,
      from: query.from,
      to: query.to,
    });
    return paginate(data, total, { page: query.page, limit: query.limit });
  }
}

export const auditService = new AuditService();