import { AdminRole } from "@repo/db";
import type { Request, Response, NextFunction } from "express";
import { AppError } from "../errors";
import type { Actor } from "../context/requestContext";

const RANK: Record<AdminRole, number> = {
  [AdminRole.READONLY]: 0,
  [AdminRole.SUPPORT]: 1,
  [AdminRole.FINANCE]: 2,
  [AdminRole.ADMIN]: 3,
  [AdminRole.OWNER]: 4,
};
export function requireRole(min : AdminRole){
    return (req : Request , res : Response , next : NextFunction)=>{
        const actor : Actor = req.actor
        if (!actor) return next(AppError.unauthorized());
        if(RANK[actor?.role as AdminRole] < RANK[min]){
            return next(AppError.forbidden(`Requires role ${min} or higher`));
        }
        next()
    }
}