export type OrderPaid = {
  orderId: string;
  userId: string;
  email: string;
  creditsGranted: number;
  currency: string;
  amountMinor: string;
};

export type RenderedEmail = { subject: string; text: string; html: string };

function formatMoney(amountMinor: string, currency: string): string {
  const minor = BigInt(amountMinor);
  const negative = minor < 0n;
  const abs = negative ? -minor : minor;
  const major = (abs / 100n).toString();
  const cents = (abs % 100n).toString().padStart(2, '0');
  return `${negative ? '-' : ''}${major}.${cents} ${currency}`;
}

export function renderReceipt(e: OrderPaid): RenderedEmail {
  const amount = formatMoney(e.amountMinor, e.currency);
  const subject = `Your PAYRAIL receipt — ${e.creditsGranted} credits`;

  const text = [
    'Thanks for your purchase!',
    '',
    `Order:   ${e.orderId}`,
    `Charged: ${amount}`,
    `Credits: ${e.creditsGranted} added to your balance`,
    '',
    '— PAYRAIL',
  ].join('\n');

  const html = `<!doctype html>
<html>
  <body style="margin:0;padding:24px;background:#f5f6f8;font-family:-apple-system,Segoe UI,Roboto,sans-serif;color:#1a1d21">
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0">
      <tr><td align="center">
        <table role="presentation" width="480" cellpadding="0" cellspacing="0" style="background:#fff;border-radius:12px;overflow:hidden;border:1px solid #e6e8eb">
          <tr><td style="background:#0b1f3a;padding:20px 28px;color:#fff;font-size:18px;font-weight:600">PAYRAIL</td></tr>
          <tr><td style="padding:28px">
            <p style="margin:0 0 16px;font-size:16px">Thanks for your purchase!</p>
            <p style="margin:0 0 4px;color:#6b7280;font-size:13px">CREDITS ADDED</p>
            <p style="margin:0 0 20px;font-size:28px;font-weight:700">${e.creditsGranted}</p>
            <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="font-size:14px">
              <tr><td style="padding:6px 0;color:#6b7280">Order</td><td align="right" style="padding:6px 0;font-family:ui-monospace,monospace">${e.orderId}</td></tr>
              <tr><td style="padding:6px 0;color:#6b7280">Charged</td><td align="right" style="padding:6px 0;font-weight:600">${amount}</td></tr>
            </table>
          </td></tr>
          <tr><td style="padding:18px 28px;background:#fafbfc;color:#9aa0a6;font-size:12px;border-top:1px solid #e6e8eb">This is an automated receipt from PAYRAIL.</td></tr>
        </table>
      </td></tr>
    </table>
  </body>
</html>`;

  return { subject, text, html };
}

export type OrderRefunded = {
  orderId: string;
  refundId: string;
  userId: string;
  email: string;
  creditsClawedBack: number;
  currency: string;
  amountMinor: string;
};

export function renderRefund(e: OrderRefunded): RenderedEmail {
  const amount = formatMoney(e.amountMinor, e.currency);
  const subject = `Your PAYRAIL refund of ${amount} is done`;

  const text = [
    'Your refund has been processed.',
    '',
    `Order:    ${e.orderId}`,
    `Refunded: ${amount}`,
    e.creditsClawedBack > 0 ? `Credits:  ${e.creditsClawedBack} removed from your balance` : '',
    '',
    'The money is on its way back to your original payment method',
    '(typically 5–7 business days, depending on your bank).',
    '',
    '— PAYRAIL',
  ]
    .filter((l) => l !== '')
    .join('\n');

  const html = `<!doctype html>
<html>
  <body style="margin:0;padding:24px;background:#f5f6f8;font-family:-apple-system,Segoe UI,Roboto,sans-serif;color:#1a1d21">
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0">
      <tr><td align="center">
        <table role="presentation" width="480" cellpadding="0" cellspacing="0" style="background:#fff;border-radius:12px;overflow:hidden;border:1px solid #e6e8eb">
          <tr><td style="background:#0b1f3a;padding:20px 28px;color:#fff;font-size:18px;font-weight:600">PAYRAIL</td></tr>
          <tr><td style="padding:28px">
            <p style="margin:0 0 16px;font-size:16px">Your refund has been processed.</p>
            <p style="margin:0 0 4px;color:#6b7280;font-size:13px">REFUNDED</p>
            <p style="margin:0 0 20px;font-size:28px;font-weight:700">${amount}</p>
            <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="font-size:14px">
              <tr><td style="padding:6px 0;color:#6b7280">Order</td><td align="right" style="padding:6px 0;font-family:ui-monospace,monospace">${e.orderId}</td></tr>
              ${e.creditsClawedBack > 0 ? `<tr><td style="padding:6px 0;color:#6b7280">Credits removed</td><td align="right" style="padding:6px 0;font-weight:600">${e.creditsClawedBack}</td></tr>` : ''}
            </table>
            <p style="margin:20px 0 0;color:#6b7280;font-size:13px">The money returns to your original payment method, typically within 5&ndash;7 business days.</p>
          </td></tr>
          <tr><td style="padding:18px 28px;background:#fafbfc;color:#9aa0a6;font-size:12px;border-top:1px solid #e6e8eb">This is an automated notice from PAYRAIL.</td></tr>
        </table>
      </td></tr>
    </table>
  </body>
</html>`;

  return { subject, text, html };
}