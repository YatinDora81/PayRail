import type { Request, Response } from "express";
import promotionsService from "./promotions.service";
import type {
  CreatePromotionInput,
  UpdatePromotionInput,
  ListPromotionsInput,
} from "./promotions.schema";

class PromotionsController {
  list = async (req: Request, res: Response) => {
    const result = await promotionsService.list(req.query as unknown as ListPromotionsInput);
    res.json(result);
  }
}

export default new PromotionsController();
