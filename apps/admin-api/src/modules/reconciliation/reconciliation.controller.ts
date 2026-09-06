import type { Request, Response } from 'express';
import { reconciliationService } from './reconciliation.service';
import type { ListReconciliationInput } from './reconciliation.schema';

class ReconciliationController {
  list = async (req: Request, res: Response) => {
    res.json(await reconciliationService.list(req.query as unknown as ListReconciliationInput));
  };

  get = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.json({ data: await reconciliationService.get(id) });
  };
}

export const reconciliationController = new ReconciliationController();