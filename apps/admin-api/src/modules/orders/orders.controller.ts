import type { Request, Response } from 'express';
import { ordersService } from './orders.service';
import type { ListOrdersInput } from './orders.schema';

class OrdersController {
  list = async (req: Request, res: Response) => {
    res.json(await ordersService.list(req.query as unknown as ListOrdersInput));
  };

  get = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.json({ data: await ordersService.get(id) });
  };

  ledger = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.json({ data: await ordersService.ledger(id) });
  };

  invoice = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.json({ data: await ordersService.invoice(id) });
  };
}

export const ordersController = new OrdersController();