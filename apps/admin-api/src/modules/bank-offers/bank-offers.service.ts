import type { Prisma } from '@payrail/db';
import { prisma } from '../../lib/prisma';
import { bankOffersRepository } from './bank-offers.repository';
import { writeAudit } from '../../lib/audit';
import { paginate, toSkipTake } from '../../lib/pagination';
import { cached, bust } from '../../lib/cache';
import { AppError } from '../../errors';
import { assertWindow } from '../coupons/coupons.service';
import type { CreateBankOfferInput, UpdateBankOfferInput, ListBankOffersInput } from './bank-offers.schema';

const itemKey = (id: string): string => `bank-offers:item:${id}`;
 
async function binRangeWrite(
  tx: Prisma.TransactionClient,
  binRangeId: string | null | undefined,
): Promise<Pick<Prisma.BankOfferUpdateInput, 'binRange'>> {
  if (binRangeId === undefined) return {};
  if (binRangeId === null) return { binRange: { disconnect: true } };
  const range = await tx.binRange.findUnique({ where: { id: binRangeId }, select: { id: true } });
  if (!range) throw AppError.notFound('BIN range not found');
  return { binRange: { connect: { id: binRangeId } } };
}

class BankOffersService {
  async create(input: CreateBankOfferInput) {
    return prisma.$transaction(async (tx) => {
      const { binRangeId, ...fields } = input;
      const created = await bankOffersRepository.create(
        { ...fields, maxDiscountMinor: fields.maxDiscountMinor ?? null, ...(await binRangeWrite(tx, binRangeId)) },
        tx,
      );
      await writeAudit({ action: 'bankOffer.create', entityType: 'BankOffer', entityId: created.id, after: created }, tx);
      return created;
    });
  }

  async get(id: string) {
    return cached(itemKey(id), 300, async () => {
      const offer = await bankOffersRepository.findById(id);
      if (!offer) throw AppError.notFound('Bank offer not found');
      return offer;
    });
  }

  async list(query: ListBankOffersInput) {
    const { skip, take } = toSkipTake(query);
    const sig = `${query.page}:${query.limit}:${query.isActive ?? 'all'}:${query.bankName ?? ''}:${query.country ?? ''}`;
    return cached(`bank-offers:list:${sig}`, 60, async () => {
      const { data, total } = await bankOffersRepository.list({ skip, take, isActive: query.isActive, bankName: query.bankName, country: query.country });
      return paginate(data, total, { page: query.page, limit: query.limit });
    });
  }

  async update(id: string, input: UpdateBankOfferInput) {
    const updated = await prisma.$transaction(async (tx) => {
      const before = await tx.bankOffer.findUnique({ where: { id } });
      if (!before) throw AppError.notFound('Bank offer not found');
      const { binRangeId, ...fields } = input;
      assertWindow(fields.startsAt ?? before.startsAt, fields.endsAt ?? before.endsAt);
      const next = await bankOffersRepository.update(id, { ...fields, ...(await binRangeWrite(tx, binRangeId)) }, tx);
      await writeAudit({ action: 'bankOffer.update', entityType: 'BankOffer', entityId: id, before, after: next }, tx);
      return next;
    });
    await bust(itemKey(id));
    return updated;
  }

  async deactivate(id: string) {
    const updated = await prisma.$transaction(async (tx) => {
      const before = await tx.bankOffer.findUnique({ where: { id } });
      if (!before) throw AppError.notFound('Bank offer not found');
      if (!before.isActive) return before;
      const next = await bankOffersRepository.update(id, { isActive: false }, tx);
      await writeAudit({ action: 'bankOffer.deactivate', entityType: 'BankOffer', entityId: id, before, after: next }, tx);
      return next;
    });
    await bust(itemKey(id));
    return updated;
  }
}

export const bankOffersService = new BankOffersService();