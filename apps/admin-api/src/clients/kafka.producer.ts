import { Kafka, Partitioners, type Producer } from "kafkajs";
import { env } from "../config/env";
import { logger } from "../lib/logger";

const kafka = new Kafka({
  clientId: "admin-api",
  brokers: env.KAFKA_BROKERS.split(",").map((b) => b.trim()),
});

const producer: Producer = kafka.producer({
  createPartitioner: Partitioners.DefaultPartitioner,
  idempotent: true,
  maxInFlightRequests: 1,
});

let connected = false;
export async function connectProducer(): Promise<void> {
  if (connected) return;
  await producer.connect();
  connected = true;
  logger.info("kafka producer connected");
}

export async function disconnectProducer(): Promise<void> {
  if (!connected) return;
  await producer.disconnect();
  connected = false;
}

async function publish(
  topic: string,
  key: string,
  payload: unknown,
): Promise<void> {
  await connectProducer();
  await producer.send({
    topic,
    messages: [{ key, value: JSON.stringify(payload) }],
  });

  logger.info({ topic, key }, "kafka event published");
}

export type BudgetUpserted = {
  promotionId: string;
  currency: string;
  capMinor: string;
};

export const KAFKA_TOPICS = {
  PromotionBudgetUpserted: "promotion.budget.upserted",
  PromotionActivated: "promotion.activated",
} as const;

export type KafkaTopic = (typeof KAFKA_TOPICS)[keyof typeof KAFKA_TOPICS];

export const emitBudgetUpserted = async (e: BudgetUpserted): Promise<void> => {
  publish(KAFKA_TOPICS.PromotionBudgetUpserted, e.promotionId, e);
};

export const emitPromotionActivated = (promotionId: string): Promise<void> =>
  publish(KAFKA_TOPICS.PromotionActivated, promotionId, { promotionId });
