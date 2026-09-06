import type { Request, Response } from 'express';
import { disputesService } from './disputes.service';
import type { SubmitEvidenceInput, AcceptDisputeInput, ListDisputesInput } from './disputes.schema';

class DisputesController {
  list = async (req: Request, res: Response) => {
    const result = await disputesService.list(req.query as unknown as ListDisputesInput);
    res.json(result);
  };

  get = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const dispute = await disputesService.get(id);
    res.json({ data: dispute });
  };

  submitEvidence = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const dispute = await disputesService.submitEvidence(id, req.body as SubmitEvidenceInput);
    res.json({ data: dispute });
  };

  accept = async (req: Request, res: Response) => {
    const { id } = req.params as { id: string };
    const dispute = await disputesService.accept(id, req.body as AcceptDisputeInput);
    res.json({ data: dispute });
  };
}

export const disputesController = new DisputesController();