import nodemailer, { type Transporter } from 'nodemailer';
import { env } from '../config/env';
import { logger } from '../lib/logger';
import type { RenderedEmail } from './templates';

function buildTransport(): { transport: Transporter; live: boolean } {
  if (env.SMTP_HOST) {
    return {
      live: true,
      transport: nodemailer.createTransport({
        host: env.SMTP_HOST,
        port: env.SMTP_PORT,
        secure: env.SMTP_PORT === 465,
        auth: env.SMTP_USER ? { user: env.SMTP_USER, pass: env.SMTP_PASS } : undefined,
      }),
    };
  }
  return { live: false, transport: nodemailer.createTransport({ jsonTransport: true }) };
}

const { transport, live } = buildTransport();

export async function sendEmail(to: string, mail: RenderedEmail): Promise<void> {
  const info = await transport.sendMail({
    from: env.MAIL_FROM,
    to,
    subject: mail.subject,
    text: mail.text,
    html: mail.html,
  });
  if (live) {
    logger.info({ to, subject: mail.subject, messageId: info.messageId }, 'email sent');
  } else {
    logger.info({ to, subject: mail.subject }, 'email rendered (dev: not sent — set SMTP_HOST to deliver)');
  }
}