import type { Request, Response, NextFunction } from 'express';
import { AdminRole } from '@payrail/db';
import { AppError } from '../errors';

const RANK: Record<AdminRole, number> = { 
  [AdminRole.READONLY]: 0,
  [AdminRole.SUPPORT]: 1,
  [AdminRole.FINANCE]: 2,
  [AdminRole.ADMIN]: 3,
  [AdminRole.OWNER]: 4,
};

export function requireRole(min: AdminRole) {
  return (req: Request, _res: Response, next: NextFunction): void => {
    const actor = req.actor;
    if (!actor) return next(AppError.unauthorized());
    if (RANK[actor?.role as AdminRole] < RANK[min]) {
      return next(AppError.forbidden(`Requires role ${min} or higher`));
    }
    next();
  };
}