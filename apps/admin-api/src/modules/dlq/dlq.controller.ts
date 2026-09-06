import type { Request, Response } from 'express';
import { dlqService } from './dlq.service';
import type { ListDlqInput } from './dlq.schema';

class DlqController {
  list = async (req: Request, res: Response) => {
    res.json(await dlqService.list(req.query as unknown as ListDlqInput));
  };

  get = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.json({ data: await dlqService.get(id) });
  };

  replay = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.status(202).json({ data: await dlqService.replay(id) });
  };
}

export const dlqController = new DlqController();