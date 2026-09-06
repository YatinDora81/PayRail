import { z } from 'zod';
import {
  Currency,
  StackingMode,
  RuleType,
  EffectType,
  CardNetwork,
  CardType,
  DisputeStatus,
  InvoiceStatus,
  OrderStatus,
  Gateway,
  BankOfferType,
  BankOfferFunding,
} from '@payrail/db';

export const zMinor = z
  .union([z.string(), z.number()])
  .superRefine((v, ctx) => {
    if (!/^\d+$/.test(String(v))) {
      ctx.addIssue({ code: 'custom', message: 'Must be a non-negative integer in minor units' });
    }
  })
  .transform((v) => BigInt(v));

export const zBps = z.number().int().min(0).max(10000);

export const zDate = z.coerce.date();

export const zCountry = z.string().regex(/^[A-Z]{2}$/, 'ISO-3166 alpha-2 country, e.g. IN');

export const zBoolQuery = z.enum(['true', 'false']).transform((v) => v === 'true');

export const IdParam = z.object({ id: z.string().min(1) });

export const zCurrency = z.enum(Currency);
export const zStackingMode = z.enum(StackingMode);
export const zRuleType = z.enum(RuleType);
export const zEffectType = z.enum(EffectType);
export const zCardNetwork = z.enum(CardNetwork);
export const zCardType = z.enum(CardType);
export const zDisputeStatus = z.enum(DisputeStatus);
export const zInvoiceStatus = z.enum(InvoiceStatus);
export const zOrderStatus = z.enum(OrderStatus);
export const zGateway = z.enum(Gateway);
export const zBankOfferType = z.enum(BankOfferType);
export const zBankOfferFunding = z.enum(BankOfferFunding);