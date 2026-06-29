import type { Gateway } from "@repo/db";
import { getContext } from "../context/requestContext";
import { AppError } from "../errors";
import { request } from "undici";
import { env } from "../config/env";

export interface CreateRefundParams {
  gateway: Gateway;
  gatewayPaymentId: string;
  amountMinor: bigint;
  currency: string;
  idempotencyKey: string;
}

export interface CreateRefundResult {
  gatewayRefundId: string;
  status: "PENDING" | "PROCESSING" | "SUCCEEDED" | "FAILED";
}

export async function createRefund(
  params: CreateRefundParams,
): Promise<CreateRefundResult> {
  const ctx = getContext();
  try {
    const res = await request(`${env.GATEWAY_URL}/v1/refunds`, {
      method: "POST",
      headersTimeout: env.GATEWAY_TIMEOUT_MS,
      bodyTimeout: env.GATEWAY_TIMEOUT_MS,
      headers: {
        "content-type": "application/json",
        "idempotency-key": params.idempotencyKey,
        ...(ctx?.traceId
          ? { traceparent: `00-${ctx.traceId}-0000000000000001-01` }
          : {}),
      },
      body: JSON.stringify({
        gateway: params.gateway,
        gatewayPaymentId: params.gatewayPaymentId,
        amountMinor: params.amountMinor.toString(),
        currency: params.currency,
      }),
    });

    if (res.statusCode >= 400) {
      const text = await res.body.text();
      throw AppError.upstream(
        `gateway-go refund failed (${res.statusCode}): ${text.slice(0, 300)}`,
      );
    }
    return (await res.body.json()) as CreateRefundResult;
  } catch (err) {
    if (err instanceof AppError) throw err;
    throw AppError.upstream("gateway-go unreachable", err);
  }
}
