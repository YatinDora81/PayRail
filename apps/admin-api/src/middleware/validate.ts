import type { Request, Response, NextFunction } from "express";
import { ZodError, type ZodTypeAny } from "zod";
import { AppError } from "../errors";

interface Schemas {
  body?: ZodTypeAny;
  query?: ZodTypeAny;
  params?: ZodTypeAny;
}

export function validate(schemas: Schemas) {
  return (req: Request, _res: Response, next: NextFunction) => {
    try {
      if (schemas.params)
        req.params = schemas.params.parse(req.params) as typeof req.params;
      if (schemas.query)
        req.query = schemas.query.parse(req.query) as typeof req.query;
      if (schemas.body) req.body = schemas.body.parse(req.body);

      next();
      
    } catch (err) {
      if (err instanceof ZodError)
        return next(AppError.validation(err.flatten()));
      next(err);
    }
  };
}
