import type { Request, Response } from 'express';
import { plansService } from './plans.service';
import type { CreatePlanInput, UpdatePlanInput, ListPlansInput } from './plans.schema';

class PlansController {
  create = async (req: Request, res: Response) => {
    const plan = await plansService.create(req.body as CreatePlanInput);
    res.status(201).json({ data: plan });
  };

  list = async (req: Request, res: Response) => {
    const result = await plansService.list(req.query as unknown as ListPlansInput);
    res.json(result);
  };

  get = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const plan = await plansService.get(id);
    res.json({ data: plan });
  };

  update = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const plan = await plansService.update(id, req.body as UpdatePlanInput);
    res.json({ data: plan });
  };

  deactivate = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const plan = await plansService.deactivate(id);
    res.json({ data: plan });
  };
}

export const plansController = new PlansController();