import type { Request, Response } from 'express';
import { invoicesService } from './invoices.service';
import type { ListInvoicesInput } from './invoices.schema';

class InvoicesController {
  list = async (req: Request, res: Response) => {
    res.json(await invoicesService.list(req.query as unknown as ListInvoicesInput));
  };

  get = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.json({ data: await invoicesService.get(id) });
  };

  pdf = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    res.redirect(302, await invoicesService.pdfUrl(id));
  };
}

export const invoicesController = new InvoicesController();