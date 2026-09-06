import type { Request, Response, NextFunction } from 'express';
import { AppError } from '../errors';

export function notFound(req: Request, _res: Response, next: NextFunction): void {
  next(AppError.notFound(`Route ${req.method} ${req.path} not found`));
}