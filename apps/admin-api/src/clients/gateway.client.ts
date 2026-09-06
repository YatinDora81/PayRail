import { request } from 'undici';
import type { Gateway } from '@payrail/db';
import { env } from '../config/env';
import { AppError } from '../errors';
import { getContext } from '../context/requestContext';

export interface CreateRefundParams {
  gateway: Gateway;
  gatewayPaymentId: string;
  gatewayOrderId: string; 
  amountMinor: bigint;
  currency: string;
  idempotencyKey: string;
}

export interface CreateRefundResult {
  gatewayRefundId: string;
}

export class GatewayRefundRejectedError extends Error {
  constructor(
    public readonly statusCode: number,
    message: string,
  ) {
    super(message);
    this.name = 'GatewayRefundRejectedError';
  }
}

export async function createRefund(params: CreateRefundParams): Promise<CreateRefundResult> {
  const ctx = getContext();
  try {
    const res = await request(`${env.GATEWAY_URL}/v1/refunds`, {
      method: 'POST',
      headersTimeout: env.GATEWAY_TIMEOUT_MS,
      bodyTimeout: env.GATEWAY_TIMEOUT_MS,
      headers: {
        'content-type': 'application/json',
        'idempotency-key': params.idempotencyKey,
        ...(ctx?.traceId ? { traceparent: `00-${ctx.traceId}-0000000000000001-01` } : {}),
      },
      body: JSON.stringify({
        gateway: params.gateway,
        gatewayPaymentId: params.gatewayPaymentId,
        gatewayOrderId: params.gatewayOrderId,
        amountMinor: params.amountMinor.toString(),
        currency: params.currency,
      }),
    });

    if (res.statusCode >= 400 && res.statusCode < 500) {
      const text = await res.body.text();
      throw new GatewayRefundRejectedError(res.statusCode, text.slice(0, 300));
    }
    if (res.statusCode >= 500) {
      const text = await res.body.text();
      throw AppError.upstream(`gateway-go refund failed (${res.statusCode}): ${text.slice(0, 300)}`);
    }
    const body = (await res.body.json()) as Partial<CreateRefundResult>;
    if (typeof body.gatewayRefundId !== 'string' || !body.gatewayRefundId) {
      throw AppError.upstream('gateway-go answered 2xx without a gatewayRefundId'); 
    }
    return { gatewayRefundId: body.gatewayRefundId };
  } catch (err) {
    if (err instanceof GatewayRefundRejectedError || err instanceof AppError) throw err;
    throw AppError.upstream('gateway-go unreachable', err);
  }
}