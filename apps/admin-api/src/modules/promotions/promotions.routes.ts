import { Router } from "express";
import { AdminRole } from "@repo/db";
import { asyncHandler } from "../../lib/asyncHandler";
import { authenticate } from "../../middleware/auth";
import { requireRole } from "../../middleware/rbac";
import { validate } from "../../middleware/validate";
import promotionsController from "./promotions.controller";
import {
  CreatePromotionSchema,
  UpdatePromotionSchema,
  ListPromotionsQuery,
  PromotionIdParam,
} from "./promotions.schema";

export const promotionsRouter: Router = Router();

promotionsRouter.use(authenticate);

promotionsRouter.get(
  '/',
  requireRole(AdminRole.READONLY),
  validate({ query: ListPromotionsQuery }),
  asyncHandler(promotionsController.list),
);