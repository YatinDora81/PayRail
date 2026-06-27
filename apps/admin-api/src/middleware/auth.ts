import type { Request, Response, NextFunction } from "express";
import { AppError } from "../errors";
import jwt from "jsonwebtoken";
import { env } from "../config/env";
import { prisma } from "@repo/db";
import { setActor, type Actor } from "../context/requestContext";

interface AdminJwtPayload {
  sub: string; // AdminUser.id
}

export async function authenticate(
  req: Request,
  _res: Response,
  next: NextFunction,
): Promise<void> {
  try {
    const header = req.header("authorization");
    if (!header?.startsWith("Bearer "))
      throw AppError.unauthorized("Missing bearer token");

    const token = header.slice("Bearer ".length).trim();
    let payload: AdminJwtPayload;

    try {
      payload = jwt.verify(token, env.ADMIN_JWT_SECRET) as AdminJwtPayload;
    } catch {
      throw AppError.unauthorized("Invalid or expired token");
    }

    const admin = await prisma.adminUser.findUnique({
      where: { id: payload.sub },
    });
    if (!admin || !admin.isActive)
      throw AppError.unauthorized("Admin account not found or inactive");

    const actor : Actor = { id: admin.id, email: admin.email, role: admin.role };
    req.actor = actor
    setActor(actor)
    next()
  } catch (error) {
    next(error);
  }
}
