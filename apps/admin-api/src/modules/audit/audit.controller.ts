import type { Request, Response } from 'express';
import { auditService } from './audit.service';
import type { ListAuditInput } from './audit.schema';

class AuditController {
  list = async (req: Request, res: Response) => {
    const result = await auditService.list(req.query as unknown as ListAuditInput);
    res.json(result);
  };

  get = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const entry = await auditService.get(id);
    res.json({ data: entry });
  };
}

export const auditController = new AuditController();
