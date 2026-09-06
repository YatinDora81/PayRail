import type { Request, Response } from 'express';
import { binRangesService } from './bin-ranges.service';
import type { CreateBinRangeInput, ListBinRangesInput } from './bin-ranges.schema';

class BinRangesController {
  create = async (req: Request, res: Response) => {
    const range = await binRangesService.create(req.body as CreateBinRangeInput);
    res.status(201).json({ data: range });
  };

  list = async (req: Request, res: Response) => {
    const result = await binRangesService.list(req.query as unknown as ListBinRangesInput);
    res.json(result);
  };

  get = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const range = await binRangesService.get(id);
    res.json({ data: range });
  };
}

export const binRangesController = new BinRangesController();