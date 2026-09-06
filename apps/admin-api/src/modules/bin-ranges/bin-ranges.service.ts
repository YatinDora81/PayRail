import { prisma } from '../../lib/prisma';
import { binRangesRepository } from './bin-ranges.repository';
import { writeAudit } from '../../lib/audit';
import { paginate, toSkipTake } from '../../lib/pagination';
import { cached } from '../../lib/cache';
import { AppError } from '../../errors';
import type { CreateBinRangeInput, ListBinRangesInput } from './bin-ranges.schema';

const itemKey = (id: string): string => `bin-ranges:item:${id}`;

class BinRangesService {
  async create(input: CreateBinRangeInput) {
    return prisma.$transaction(async (tx) => {
      const created = await binRangesRepository.create(
        {
          bankName: input.bankName,
          network: input.network,
          binLow: input.binLow,
          binHigh: input.binHigh,
          cardType: input.cardType,
          isActive: input.isActive,
        },
        tx,
      );
      await writeAudit({ action: 'binRange.create', entityType: 'BinRange', entityId: created.id, after: created }, tx);
      return created;
    });
  }

  async get(id: string) {
    return cached(itemKey(id), 3600, async () => {
      const range = await binRangesRepository.findById(id);
      if (!range) throw AppError.notFound('BIN range not found');
      return range;
    });
  }

  async list(query: ListBinRangesInput) {
    const { skip, take } = toSkipTake(query);
    const sig = `${query.page}:${query.limit}:${query.isActive ?? 'all'}:${query.network ?? ''}:${query.bankName ?? ''}`;
    return cached(`bin-ranges:list:${sig}`, 600, async () => { 
      const { data, total } = await binRangesRepository.list({ skip, take, isActive: query.isActive, network: query.network, bankName: query.bankName });
      return paginate(data, total, { page: query.page, limit: query.limit });
    });
  }
}

export const binRangesService = new BinRangesService();