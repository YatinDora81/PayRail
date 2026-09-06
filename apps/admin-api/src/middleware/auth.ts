import type { Request, Response, NextFunction } from 'express';
import jwt from 'jsonwebtoken';
import type { AdminRole } from '@payrail/db';
import { env } from '../config/env';
import { prisma } from '../lib/prisma';
import * as cache from '../lib/cache';
import { AppError } from '../errors';
import { setActor, type Actor } from '../context/requestContext';

interface AdminJwtPayload {
  sub?: unknown; // AdminUser.id
}

interface CachedActor {
  id: string;
  role: AdminRole;
  isActive: boolean;
}

const actorKey = (id: string): string => `admin:${id}`;

async function loadActor(id: string): Promise<CachedActor | null> {
  const ttl = env.ADMIN_ACTOR_CACHE_TTL_S;
  if (ttl > 0) {
    const hit = await cache.get<CachedActor>(actorKey(id));
    if (hit) return hit;
  }
  const admin = await prisma.adminUser.findUnique({
    where: { id },
    select: { id: true, role: true, isActive: true },
  });
  if (admin && ttl > 0) await cache.set(actorKey(id), admin, ttl);
  return admin;
}

export async function authenticate(req: Request, _res: Response, next: NextFunction): Promise<void> {
  try {
    const header = req.header('authorization');
    if (!header?.startsWith('Bearer ')) throw AppError.unauthorized('Missing bearer token');
    const token = header.slice('Bearer '.length).trim();

    let payload: AdminJwtPayload;
    try {
      payload = jwt.verify(token, env.ADMIN_JWT_SECRET, { algorithms: ['HS256'] }) as AdminJwtPayload;
    } catch {
      throw AppError.unauthorized('Invalid or expired token');
    }
    if (typeof payload.sub !== 'string' || payload.sub.length === 0) {
      throw AppError.unauthorized('Token has no subject');
    }

    const admin = await loadActor(payload.sub);
    if (!admin || !admin.isActive) throw AppError.unauthorized('Admin account not found or inactive');

    const actor: Actor = { id: admin.id, role: admin.role };
    req.actor = actor;
    setActor(actor);
    next();
  } catch (err) {
    next(err);
  }
}