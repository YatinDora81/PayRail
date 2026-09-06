import type { Request, Response } from 'express';
import { refundsService } from './refunds.service';
import type { CreateRefundInput } from './refunds.schema';

class RefundsController {
  create = async (req: Request, res: Response) => {
    const idempotencyKey = req.header('idempotency-key') as string;
    const refund = await refundsService.create(req.body as CreateRefundInput, idempotencyKey);
    res.status(201).json({ data: refund });
  };

  get = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const refund = await refundsService.get(id);
    res.json({ data: refund });
  };
}

export const refundsController = new RefundsController();