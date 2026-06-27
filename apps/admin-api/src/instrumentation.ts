import { NodeSDK } from "@opentelemetry/sdk-node";
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-grpc";
import { getNodeAutoInstrumentations } from "@opentelemetry/auto-instrumentations-node";

const sdk = new NodeSDK({
  traceExporter: new OTLPTraceExporter(),
  instrumentations: [
    getNodeAutoInstrumentations({
      "@opentelemetry/instrumentation-fs": { enabled: false }, // it is not that important thats why we have not enabled
    }),
  ],
});

sdk.start();

const shutdown = async (): Promise<void> => {
  try {
    await sdk.shutdown();
  } catch (err) {
    // eslint-disable-next-line no-console
    console.error("otel shutdown failed", err);
  }
};

process.once("SIGTERM", () => void shutdown());
process.once("SIGINT", () => void shutdown());
