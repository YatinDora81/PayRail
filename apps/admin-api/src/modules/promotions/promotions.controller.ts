import type { Request, Response } from 'express';
import { promotionsService } from './promotions.service';
import type {
  CreatePromotionInput,
  UpdatePromotionInput,
  ListPromotionsInput,
  UpsertPromotionBudgetInput,
  BudgetsQueryInput,
  CreatePromotionCouponInput,
} from './promotions.schema';

class PromotionsController {
  create = async (req: Request, res: Response) => {
    const promotion = await promotionsService.create(req.body as CreatePromotionInput);
    res.status(201).json({ data: promotion });
  };

  list = async (req: Request, res: Response) => {
    const result = await promotionsService.list(req.query as unknown as ListPromotionsInput);
    res.json(result);
  };

  get = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.json({ data: await promotionsService.get(id) });
  };

  update = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.json({ data: await promotionsService.update(id, req.body as UpdatePromotionInput) });
  };

  activate = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.json({ data: await promotionsService.activate(id) });
  };

  deactivate = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.json({ data: await promotionsService.deactivate(id) });
  };

  upsertBudget = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.json({ data: await promotionsService.upsertBudget(id, req.body as UpsertPromotionBudgetInput) });
  };

  budgets = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const { currency } = req.query as unknown as BudgetsQueryInput;
    res.json({ data: await promotionsService.budgets(id, currency) });
  };

  createCoupon = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.status(201).json({ data: await promotionsService.createCoupon(id, req.body as CreatePromotionCouponInput) });
  };

  listCoupons = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.json({ data: await promotionsService.listCoupons(id) });
  };
}

export const promotionsController = new PromotionsController();