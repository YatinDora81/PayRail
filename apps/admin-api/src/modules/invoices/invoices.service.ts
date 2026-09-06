import { invoicesRepository } from './invoices.repository';
import { paginate, toSkipTake } from '../../lib/pagination';
import { AppError } from '../../errors';
import type { ListInvoicesInput } from './invoices.schema';
 
export const formatInvoiceNumber = (series: string, number: number): string =>
  `INV-${series}-${String(number).padStart(6, '0')}`;

export const withInvoiceNumber = <T extends { series: string; number: number }>(inv: T) => ({
  ...inv,
  invoiceNumber: formatInvoiceNumber(inv.series, inv.number),
});

class InvoicesService {
  async get(id: string) {
    const invoice = await invoicesRepository.findById(id);
    if (!invoice) throw AppError.notFound('Invoice not found');
    return withInvoiceNumber(invoice);
  }

  async list(query: ListInvoicesInput) {
    const { skip, take } = toSkipTake(query);
    const { data, total } = await invoicesRepository.list({
      skip,
      take,
      status: query.status,
      orderId: query.orderId,
      series: query.series,
      issuedFrom: query.issuedFrom,
      issuedTo: query.issuedTo,
    });
    return paginate(data.map(withInvoiceNumber), total, { page: query.page, limit: query.limit });
  }
 
  async pdfUrl(id: string): Promise<string> {
    const invoice = await invoicesRepository.findById(id);
    if (!invoice) throw AppError.notFound('Invoice not found');
    if (!invoice.pdfUrl) throw AppError.notFound('Invoice PDF not rendered yet');
    return invoice.pdfUrl;
  }
}

export const invoicesService = new InvoicesService();