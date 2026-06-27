import { AsyncLocalStorage } from "node:async_hooks";
import type { Logger } from "pino";

export interface Actor {
  id: string;
  email: string;
  role: string;
}

export interface RequestContext {
  traceId: string;
  ip?: string;
  userAgent?: string;
  actor: Actor;
  logger: Logger;
}

const storage = new AsyncLocalStorage<RequestContext>();

export function runWithContext<T>(ctx: RequestContext, fn: () => T): T {
  return storage.run(ctx, fn);
}

export function getContext(): RequestContext | undefined {
  return storage.getStore();
}

export function requireContext(): RequestContext {
  const ctx = storage.getStore();
  if (!ctx)
    throw new Error("RequestContext accessed outside of a request scope");
  return ctx;
}

export function setActor(actor : Actor){
    const ctx = storage.getStore()
    if(ctx) ctx.actor = actor
}
