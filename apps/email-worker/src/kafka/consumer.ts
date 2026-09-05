import { Kafka, type Consumer } from 'kafkajs';
import { env } from '../config/env';
import { logger } from '../lib/logger';

const kafka = new Kafka({
  clientId: 'email-worker',
  brokers: env.KAFKA_BROKERS.split(',').map((b) => b.trim()),
});

export type MessageHandler = (topic: string, value: string) => Promise<void>;

export async function startConsumer(topics: string[], handle: MessageHandler): Promise<Consumer> {
  const consumer = kafka.consumer({ groupId: env.KAFKA_GROUP_ID });
  await consumer.connect();
  for (const topic of topics) {
    await consumer.subscribe({ topic, fromBeginning: false });
  }
  logger.info({ topics, groupId: env.KAFKA_GROUP_ID }, 'kafka consumer subscribed');

  await consumer.run({
    eachMessage: async ({ topic, message }) => {
      const value = message.value?.toString();
      if (!value) return; 
      await handle(topic, value);
    },
  });

  return consumer;
}