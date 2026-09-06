import type { Request, Response } from 'express';
import { couponsService } from './coupons.service';
import type { CreateCouponInput, UpdateCouponInput, ListCouponsInput } from './coupons.schema';

class CouponsController {
  create = async (req: Request, res: Response) => {
    const coupon = await couponsService.create(req.body as CreateCouponInput);
    res.status(201).json({ data: coupon });
  };

  list = async (req: Request, res: Response) => {
    const result = await couponsService.list(req.query as unknown as ListCouponsInput);
    res.json(result);
  };

  get = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const coupon = await couponsService.get(id);
    res.json({ data: coupon });
  };

  update = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const coupon = await couponsService.update(id, req.body as UpdateCouponInput);
    res.json({ data: coupon });
  };

  deactivate = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const coupon = await couponsService.deactivate(id);
    res.json({ data: coupon });
  };
}

export const couponsController = new CouponsController();