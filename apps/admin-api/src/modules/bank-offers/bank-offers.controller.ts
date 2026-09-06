import type { Request, Response } from 'express';
import { bankOffersService } from './bank-offers.service';
import type { CreateBankOfferInput, UpdateBankOfferInput, ListBankOffersInput } from './bank-offers.schema';

class BankOffersController {
  create = async (req: Request, res: Response) => {
    const offer = await bankOffersService.create(req.body as CreateBankOfferInput);
    res.status(201).json({ data: offer });
  };

  list = async (req: Request, res: Response) => {
    const result = await bankOffersService.list(req.query as unknown as ListBankOffersInput);
    res.json(result);
  };

  get = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const offer = await bankOffersService.get(id);
    res.json({ data: offer });
  };

  update = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const offer = await bankOffersService.update(id, req.body as UpdateBankOfferInput);
    res.json({ data: offer });
  };

  deactivate = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const offer = await bankOffersService.deactivate(id);
    res.json({ data: offer });
  };
}

export const bankOffersController = new BankOffersController();