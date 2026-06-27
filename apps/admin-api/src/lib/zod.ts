import { z } from "zod";
import { Currency, StackingMode, RuleType, EffectType } from "@repo/db";
// import {} from './prisma'

export const zMinor = z
  .union([z.string(), z.number()])
  .superRefine((v, ctx) => {
    if (!/^\d+$/.test(String(v))) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: "Must be a non-negative integer in minor units",
      });
    }
  })
  .transform((v) => BigInt(v));

export const zBps = z.number().int().min(0).max(10000);

// ISO-8601 string
export const zDate = z.coerce.date();

export const zCurrency = z.nativeEnum(Currency);
export const zStackingMode = z.nativeEnum(StackingMode);
export const zRuleType = z.nativeEnum(RuleType);
export const zEffectType = z.nativeEnum(EffectType);
