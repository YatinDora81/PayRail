import { ordersRepository } from './orders.repository';
import { withInvoiceNumber } from '../invoices/invoices.service';
import { paginate, toSkipTake } from '../../lib/pagination';
import { AppError } from '../../errors';
import type { ListOrdersInput } from './orders.schema';

class OrdersService {
  async get(id: string) {
    const order = await ordersRepository.findById(id);
    if (!order) throw AppError.notFound('Order not found');
    return { ...order, invoice: order.invoice ? withInvoiceNumber(order.invoice) : null };
  }

  async list(query: ListOrdersInput) {
    const { skip, take } = toSkipTake(query);
    const { data, total } = await ordersRepository.list({
      skip,
      take,
      status: query.status,
      userId: query.userId,
      gateway: query.gateway,
      gatewayOrderId: query.gatewayOrderId,
      from: query.from,
      to: query.to,
    });
    return paginate(data, total, { page: query.page, limit: query.limit });
  }

  async ledger(id: string) {
    const order = await ordersRepository.findById(id);
    if (!order) throw AppError.notFound('Order not found');
    return ordersRepository.ledger(
      order.id,
      order.refunds.map((r) => r.id),
      order.disputes.map((d) => d.id),
    );
  }

  async invoice(id: string) {
    const order = await ordersRepository.findById(id);
    if (!order) throw AppError.notFound('Order not found');
    if (!order.invoice) throw AppError.notFound('Invoice not assigned yet');
    return withInvoiceNumber(order.invoice);
  }
}

export const ordersService = new OrdersService();