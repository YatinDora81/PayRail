export type ApiErrorCode =
  | "BAD_REQUEST"
  | "VALIDATION_ERROR"
  | "UNAUTHORIZED"
  | "FORBIDDEN"
  | "NOT_FOUND"
  | "CONFLICT"
  | "IDEMPOTENCY_CONFLICT"
  | "UPSTREAM_ERROR"
  | "INTERNAL";

interface AppErrorOptios {
  details?: unknown;
  expose?: boolean;
  cause?: unknown;
}

export class AppError extends Error {
  readonly status: number;
  readonly code: ApiErrorCode;
  readonly details?: unknown;
  readonly expose: boolean;

  constructor(
    status: number,
    code: ApiErrorCode,
    message: string,
    opts: AppErrorOptios = {},
  ) {
    super(
      message,
      opts.cause !== undefined ? { cause: opts.cause } : undefined,
    );
    this.name = "AppError";
    this.status = status;
    this.code = code;
    this.details = opts.details;
    this.expose = opts.expose ?? status < 500;
    Error.captureStackTrace?.(this, AppError);
  }

  static badRequest(message = "Bad request", details?: unknown): AppError {
    return new AppError(400, "BAD_REQUEST", message, { details });
  }

  static validation(details: unknown, message = "Validation failed"): AppError {
    return new AppError(422, "VALIDATION_ERROR", message, { details });
  }

  static unauthorized(message = "Unauthorized"): AppError {
    return new AppError(401, "UNAUTHORIZED", message);
  }

  static forbidden(message = "Forbidden"): AppError {
    return new AppError(403, "FORBIDDEN", message);
  }

  static notFound(message = "Not found"): AppError {
    return new AppError(404, "NOT_FOUND", message);
  }

  static conflict(
    message = "Conflict",
    code: ApiErrorCode = "CONFLICT",
  ): AppError {
    return new AppError(409, code, message);
  }

  static upstream(
    message = "Upstream service error",
    cause?: unknown,
  ): AppError {
    return new AppError(502, "UPSTREAM_ERROR", message, { cause });
  }

  static internal(
    message = "Internal server error",
    cause?: unknown,
  ): AppError {
    return new AppError(500, "INTERNAL", message, { cause, expose: false });
  }
  
}
