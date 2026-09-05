import { prisma } from '../lib/prisma';
import { sendEmail } from './mailer';
import type { RenderedEmail } from './templates';

export type TransactionalEmail = {
  template: 'ORDER_PAID' | 'REFUND_DONE';
  refType: 'ORDER' | 'REFUND';
  refId: string;
  to: string;
  mail: RenderedEmail;
};

const keyOf = (e: TransactionalEmail) => ({
  template_refType_refId: { template: e.template, refType: e.refType, refId: e.refId },
});

export async function sendTransactional(e: TransactionalEmail): Promise<void> {
  try {
    await prisma.emailLog.create({
      data: { template: e.template, refType: e.refType, refId: e.refId, to: e.to, status: 'PENDING' },
    });
  } catch (err) {
    if ((err as { code?: string })?.code !== 'P2002') throw err;
    const existing = await prisma.emailLog.findUnique({ where: keyOf(e) });
    if (existing?.status === 'SENT') return; 
  }
  try {
    await sendEmail(e.to, e.mail);
  } catch (err) {
    await prisma.emailLog.update({ where: keyOf(e), data: { status: 'FAILED' } }).catch(() => undefined);
    throw err;
  }
  await prisma.emailLog.update({ where: keyOf(e), data: { status: 'SENT' } });
}