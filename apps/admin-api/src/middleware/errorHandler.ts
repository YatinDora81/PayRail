import type { Request, Response, NextFunction } from "express";
import { Prisma } from "@repo/db";
import { AppError, type ApiErrorCode } from "../errors";
import { getContext } from "../context/requestContext";
import { logger } from "../lib/logger";
import { isProd } from "../config/env";

function fromPrisma(err: Prisma.PrismaClientKnownRequestError): AppError {
  switch (err.code) {
    case "P2002":
      return AppError.conflict(
        "A record with these unique fields already exists",
      );
    case "P2025":
      return AppError.notFound("Record not found");
    case "P2003":
      return AppError.badRequest(
        "Referenced record does not exist (foreign key)",
      );
    default:
      return AppError.internal("Database error", err);
  }
}

export function errorHandler(
  err: unknown,
  req: Request,
  res: Response,
  _next: NextFunction,
): void {
  let appErr: AppError;
  if (err instanceof AppError) appErr = err;
  else if (err instanceof Prisma.PrismaClientKnownRequestError)
    appErr = fromPrisma(err);
  else if (err instanceof Prisma.PrismaClientValidationError)
    appErr = AppError.badRequest("Invalid query");
  else appErr = AppError.internal("Unexpected error", err);

  const log = getContext()?.logger ?? logger;
  const meta = { err, code: appErr.code, status: appErr.status };
  if (appErr.status >= 500) log.error(meta, appErr.message);
  else log.warn(meta, appErr.message);

  const body: {
    error: {
      code: ApiErrorCode;
      message: string;
      traceId: string;
      details?: unknown;
      debug?: string;
    };
  } = {
    error: {
      code: appErr.code,
      message: appErr.expose ? appErr.message : "Internal server error",
      traceId: req.traceId,
    },
  };

  if (appErr.expose && appErr.details !== undefined) body.error.details = appErr.details;
  if (!isProd && !appErr.expose && err instanceof Error) body.error.debug = err.message;

  res.status(appErr.status).json(body)
}
