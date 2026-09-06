import type { Prisma } from '@payrail/db';
import { getContext } from '../context/requestContext';

export async function enqueueOutbox(
  tx: Prisma.TransactionClient,
  topic: string,
  partitionKey: string,
  payload: Prisma.InputJsonObject,
): Promise<void> {
  const traceId = getContext()?.traceId;
  const headers: Prisma.InputJsonObject = {
    ...(traceId ? { traceId } : {}),
    ...(traceId && /^[0-9a-f]{32}$/i.test(traceId) ? { traceparent: `00-${traceId}-0000000000000001-01` } : {}),
  };
  await tx.outboxEvent.create({ data: { topic, partitionKey, payload, headers } });
}