declare global {
  namespace Express {
    interface Request {
      traceId: string;
      actor?: Actor;
    }
  }
  interface BigInt {
    toJSON(): string;
  }
}

export {};
