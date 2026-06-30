/* ============================================================================
 * Payrail — database seed
 * ----------------------------------------------------------------------------
 * Flash-sale credit-pack payment platform. This seed fills EVERY table with
 * realistic, internally-consistent data so you can build / test the
 * checkout-api against something that looks like production.
 *
 * Money invariant honoured everywhere:
 *     finalAmountMinor = baseAmountMinor - discountAmountMinor
 *                        - bankDiscountMinor + taxAmountMinor
 *
 * The seed is RE-RUNNABLE: it wipes all tables (FK-safe order) before inserting,
 * and uses a deterministic PRNG so you get the same data every run.
 *
 * Caches (User.creditsBalance, PromotionBudget.spentMinorCached,
 * CouponCode.redeemedCount, BankOffer.spentMinorCached) are RECONCILED from the
 * underlying ledgers at the end — exactly like the real reconciler does.
 * ----------------------------------------------------------------------------
 * >>> CHECKOUT-API EDGE FIXTURES (section 8.5, named + deterministic) <<<
 * Everything the base seed lacked to exercise checkout-api's *hard* paths. Each
 * one is reachable by a stable handle so integration tests don't fish through the
 * random bulk orders. They're inserted BEFORE reconciliation, so the near-cap
 * caches fall out of the real ledgers (the reconciler confirms them, ledger wins):
 *
 *   fresh users 14/15 ........ zero orders  -> FIRST_PURCHASE + empty /me/credits
 *   plan "INR-Only Pack" ..... priced only in INR -> ResolvePricing US-fallback miss
 *   promo MEGA60 (6000bps) ... 60% on "scale" (cap 5000) -> maxDiscountBps clamp BITES
 *   promo EDGE (budgetedge) .. INR budget ~₹10 from cap -> DECRBY ErrExhausted / race
 *   coupon NEARCAP ........... 4/5 used (+ MAX_USES_GLOBAL at brink) -> one slot left
 *   coupon MAXEDOUT .......... 3/3 used -> checkout must REJECT
 *   bank offer "inmerch" ..... merchant-funded, ~₹500 from budgetCap -> cap exhaustion
 *   BIN 360001 ............... active DINERS whose only offer is expired -> reject path
 *   user 0 (intended VIP) .... vip10 USER_SEGMENT positive (segment computed in code)
 *   order "checkout-collision-demo" .. fixed idemKey -> @@unique(userId,key) race
 *   IdempotencyRecord IN_PROGRESS .... claim-first in-flight -> 409-while-in-progress
 *
 * Segment membership ("VIP") is DERIVED by code logic at checkout (e.g. order
 * velocity), never stored on User — so there's no segment column to seed.
 * REQUIRES one schema addition (implied by the HLD hardening):
 *   enum IdempotencyStatus  { IN_PROGRESS DONE FAILED }
 *   model IdempotencyRecord { status IdempotencyStatus @default(IN_PROGRESS) }
 * ----------------------------------------------------------------------------
 * Import path assumes:  schema at  prisma/schema.prisma  (output "../generated/prisma")
 *                       this file at  prisma/seed.ts
 * If you're on Prisma 7 (driver adapters required) see the note at the BOTTOM.
 * ==========================================================================*/

import "dotenv/config";
import { PrismaPg } from "@prisma/adapter-pg";
import { PrismaClient } from "../generated/prisma/client";
import type {
  Currency,
  StackingMode,
  RuleType,
  EffectType,
  Gateway,
  OrderStatus,
  DisputeStatus,
  CreditReason,
  LedgerReferenceType,
  BankOfferType,
  BankOfferFunding,
  InstrumentType,
  CardNetwork,
} from "../generated/prisma/client";

const adapter = new PrismaPg({ connectionString: `${process.env.DATABASE_URL}` });
const prisma = new PrismaClient({ adapter });

/* ----------------------------------------------------------------------------
 * tiny helpers
 * --------------------------------------------------------------------------*/
const B = (n: number): bigint => BigInt(Math.round(n)); // number -> BigInt minor units
const DAY = 86_400_000;
const at = (daysFromNow: number): Date => new Date(Date.now() + daysFromNow * DAY);

// deterministic PRNG so the seed is reproducible
let _rng = 1234567;
const rand = (): number => {
  _rng = (_rng * 1103515245 + 12345) & 0x7fffffff;
  return _rng / 0x7fffffff;
};
const randInt = (min: number, max: number): number =>
  Math.floor(min + rand() * (max - min + 1));
const choice = <T>(arr: T[]): T => arr[Math.floor(rand() * arr.length)]!;

// monotonic short ids for gateway refs / idempotency keys / event ids
let _ctr = 0;
const sid = (prefix = ""): string =>
  `${prefix}${(++_ctr).toString(36)}${Math.floor(rand() * 1e6).toString(36)}`;

// rough FX (per 1 USD), all currencies here are 2-dp minor units
const FX: Record<string, number> = { USD: 1, INR: 83, EUR: 0.92, GBP: 0.79, SGD: 1.35, AED: 3.67 };
const fromUsd = (usdMinor: number, cur: string): number => Math.round(usdMinor * (FX[cur] ?? 1));

// currency <-> canonical market <-> usable gateways
const MARKETS: { currency: Currency; country: string; gateways: Gateway[] }[] = [
  { currency: "INR", country: "IN", gateways: ["RAZORPAY", "CASHFREE"] },
  { currency: "USD", country: "US", gateways: ["STRIPE", "PAYPAL"] },
  { currency: "EUR", country: "DE", gateways: ["STRIPE", "PAYPAL"] },
  { currency: "GBP", country: "GB", gateways: ["STRIPE", "PAYPAL"] },
  { currency: "SGD", country: "SG", gateways: ["PAYPAL", "STRIPE"] },
  { currency: "AED", country: "AE", gateways: ["PAYPAL"] },
];

const SELLER_STATE = "29"; // Karnataka — intra-state => CGST+SGST, else IGST

// fresh users live at the TAIL of the users array and are NEVER touched by the
// bulk loop or scenarios — they exist so FIRST_PURCHASE / empty-credits are clean.
const NUM_FRESH_USERS = 2;

async function main() {
  console.log("🌱  Seeding Payrail …");

  /* ==========================================================================
   * 0. WIPE (children first, FK-safe)
   * ========================================================================*/
  await prisma.adminAuditLog.deleteMany();
  await prisma.reconciliationLog.deleteMany();
  await prisma.deadLetterEvent.deleteMany();
  await prisma.idempotencyRecord.deleteMany();
  await prisma.emailLog.deleteMany();
  await prisma.webhookEvents.deleteMany();
  await prisma.invoice.deleteMany();
  await prisma.orderBankOffer.deleteMany();
  await prisma.orderDiscount.deleteMany();
  await prisma.creditsLedger.deleteMany();
  await prisma.dispute.deleteMany();
  await prisma.refund.deleteMany();
  await prisma.payment.deleteMany();
  await prisma.promotionSpend.deleteMany();
  await prisma.promotionUsage.deleteMany();
  await prisma.promotionBudget.deleteMany();
  await prisma.promotionEffects.deleteMany();
  await prisma.promotionRules.deleteMany();
  await prisma.couponCode.deleteMany();
  await prisma.order.deleteMany();
  await prisma.binRange.deleteMany();
  await prisma.bankOffer.deleteMany();
  await prisma.planPrice.deleteMany();
  await prisma.plans.deleteMany();
  await prisma.paymentGatewayConfig.deleteMany();
  await prisma.adminUser.deleteMany();
  await prisma.promotions.deleteMany();
  await prisma.user.deleteMany();

  /* ==========================================================================
   * 1. GATEWAY CONFIG  (drives the selector)
   * ========================================================================*/
  await prisma.paymentGatewayConfig.createMany({
    data: [
      {
        gateway: "RAZORPAY",
        isActive: true,
        priority: 100,
        supportedCurrencies: ["INR"],
        config: { mode: "live", webhookPath: "/webhooks/razorpay", captureMode: "automatic", secretRef: "vault://payrail/razorpay" },
      },
      {
        gateway: "STRIPE",
        isActive: true,
        priority: 90,
        supportedCurrencies: ["USD", "EUR", "GBP", "SGD", "AED"],
        config: { mode: "live", webhookPath: "/webhooks/stripe", captureMode: "automatic", secretRef: "vault://payrail/stripe" },
      },
      {
        gateway: "CASHFREE",
        isActive: true,
        priority: 80,
        supportedCurrencies: ["INR"],
        config: { mode: "live", webhookPath: "/webhooks/cashfree", captureMode: "automatic", secretRef: "vault://payrail/cashfree" },
      },
      {
        gateway: "PAYPAL",
        isActive: true,
        priority: 70,
        supportedCurrencies: ["USD", "EUR", "GBP", "SGD", "AED"],
        config: { mode: "live", webhookPath: "/webhooks/paypal", intent: "CAPTURE", secretRef: "vault://payrail/paypal" },
      },
    ],
  });

  /* ==========================================================================
   * 2. ADMIN USERS  (RBAC — admin-api comes later, but seed the accounts)
   * ========================================================================*/
  const admins = await Promise.all([
    prisma.adminUser.create({ data: { email: "owner@payrail.dev", name: "Priya Owner", role: "OWNER" } }),
    prisma.adminUser.create({ data: { email: "finance@payrail.dev", name: "Rahul Finance", role: "FINANCE" } }),
    prisma.adminUser.create({ data: { email: "support@payrail.dev", name: "Sara Support", role: "SUPPORT" } }),
    prisma.adminUser.create({ data: { email: "viewer@payrail.dev", name: "Read Only", role: "READONLY" } }),
  ]);

  /* ==========================================================================
   * 3. USERS
   *    - "VIP" is NOT stored on the user — segment membership is DERIVED at
   *      checkout by code logic (e.g. ≥30 orders/month ⇒ VIP). Users 0 & 1 are
   *      the intended VIPs for vip10's USER_SEGMENT rule; the engine computes it.
   *    - users 14 & 15 are FRESH (no orders) — excluded from bulk below
   * ========================================================================*/
  const userDefs: { email: string; name: string; phone: string }[] = [
    { email: "aarav.sharma@example.in", name: "Aarav Sharma", phone: "+919800000001" }, // 0  (intended VIP)
    { email: "diya.patel@example.in", name: "Diya Patel", phone: "+919800000002" },      // 1  (intended VIP)
    { email: "vihaan.reddy@example.in", name: "Vihaan Reddy", phone: "+919800000003" },                  // 2
    { email: "ananya.iyer@example.in", name: "Ananya Iyer", phone: "+919800000004" },                    // 3
    { email: "kabir.nair@example.in", name: "Kabir Nair", phone: "+919800000005" },                      // 4
    { email: "ishita.rao@example.in", name: "Ishita Rao", phone: "+919800000006" },                      // 5
    { email: "john.carter@example.com", name: "John Carter", phone: "+14155550101" },                    // 6  US
    { email: "emily.stone@example.com", name: "Emily Stone", phone: "+14155550102" },                    // 7  US
    { email: "liam.walsh@example.co.uk", name: "Liam Walsh", phone: "+447700900001" },                   // 8  UK
    { email: "olivia.bauer@example.de", name: "Olivia Bauer", phone: "+4915110000001" },                 // 9  DE
    { email: "wei.tan@example.sg", name: "Wei Tan", phone: "+6580000001" },                              // 10 SG
    { email: "fatima.alali@example.ae", name: "Fatima Al Ali", phone: "+971500000001" },                 // 11 AE
    { email: "noah.murphy@example.com", name: "Noah Murphy", phone: "+14155550103" },                    // 12 US
    { email: "saanvi.menon@example.in", name: "Saanvi Menon", phone: "+919800000007" },                  // 13
    // ---- FRESH USERS (no orders, no ledger — first-purchase / empty-credits fixtures) ----
    { email: "fresh.first@example.in", name: "Fresh First", phone: "+919800000099" },                    // 14 fresh IN
    { email: "fresh.second@example.com", name: "Fresh Second", phone: "+14155550199" },                  // 15 fresh US
  ];
  const users: { id: string; email: string }[] = [];
  for (const u of userDefs) users.push(await prisma.user.create({ data: u }));

  /* ==========================================================================
   * 4. PLANS  (credit packs) + GEO PRICING
   * ========================================================================*/
  const planDefs = [
    { key: "lite", name: "Lite Pack", desc: "100 credits — try it out", credits: 100, usd: 199, maxBps: 10000, active: true },
    { key: "starter", name: "Starter Pack", desc: "500 credits for hobby projects", credits: 500, usd: 900, maxBps: 10000, active: true },
    { key: "creator", name: "Creator Pack", desc: "1,200 credits for regular use", credits: 1200, usd: 1900, maxBps: 8000, active: true },
    { key: "pro", name: "Pro Pack", desc: "3,000 credits for power users", credits: 3000, usd: 3900, maxBps: 7000, active: true },
    { key: "studio", name: "Studio Pack", desc: "8,000 credits for teams", credits: 8000, usd: 8900, maxBps: 6000, active: true },
    { key: "scale", name: "Scale Pack", desc: "20,000 credits, best value", credits: 20000, usd: 19900, maxBps: 5000, active: true },
    { key: "legacy", name: "Legacy Pack (v1)", desc: "Retired pack — kept for old orders", credits: 300, usd: 500, maxBps: 10000, active: false },
  ];

  const planByKey: Record<string, { id: string; credits: number; maxBps: number }> = {};
  for (const p of planDefs) {
    const created = await prisma.plans.create({
      data: {
        name: p.name,
        description: p.desc,
        credits: p.credits,
        isActive: p.active,
        maxDiscountBps: p.maxBps,
        sacCode: "998314", // GST SAC for IT/online services
      },
    });
    planByKey[p.key] = { id: created.id, credits: p.credits, maxBps: p.maxBps };
  }

  // build PlanPrice rows + an in-memory resolver:  key = planId|currency|country|city
  const priceRows: any[] = [];
  const priceLookup = new Map<string, number>();
  const addPrice = (
    planId: string, country: string, city: string, currency: string, amountMinor: number, isActive = true
  ) => {
    priceRows.push({ planId, country, city, currency, amountMinor: B(amountMinor), isActive });
    priceLookup.set(`${planId}|${currency}|${country}|${city}`, amountMinor);
  };

  for (const p of planDefs) {
    const id = planByKey[p.key]!.id;
    if (!p.active) {
      // legacy: only IN + US, inactive
      addPrice(id, "IN", "", "INR", fromUsd(p.usd, "INR"), false);
      addPrice(id, "US", "", "USD", p.usd, false);
      continue;
    }
    // country-default prices for each market
    addPrice(id, "IN", "", "INR", fromUsd(p.usd, "INR"));
    addPrice(id, "US", "", "USD", p.usd);
    addPrice(id, "DE", "", "EUR", fromUsd(p.usd, "EUR"));
    addPrice(id, "GB", "", "GBP", fromUsd(p.usd, "GBP"));
    addPrice(id, "SG", "", "SGD", fromUsd(p.usd, "SGD"));
    addPrice(id, "AE", "", "AED", fromUsd(p.usd, "AED"));
  }
  // city-level overrides (exercise: exact (country,city) beats country default)
  addPrice(planByKey["pro"]!.id, "IN", "Bengaluru", "INR", Math.round(fromUsd(3900, "INR") * 0.95)); // -5% regional
  addPrice(planByKey["scale"]!.id, "US", "San Francisco", "USD", Math.round(19900 * 1.15));          // +15%
  await prisma.planPrice.createMany({ data: priceRows });

  const resolvePrice = (planId: string, currency: string, country: string, city = ""): number => {
    const exact = priceLookup.get(`${planId}|${currency}|${country}|${city}`);
    if (exact != null) return exact;
    const def = priceLookup.get(`${planId}|${currency}|${country}|`);
    if (def != null) return def;
    throw new Error(`no price for ${planId} ${currency} ${country}/${city}`);
  };

  /* ==========================================================================
   * 5. PROMOTIONS  (+ rules, effects, coupons, budgets)
   *    covers every StackingMode, RuleType and EffectType
   *    (+ four checkout edge promos: mega60 / budgetedge / couponnear / couponmax)
   * ========================================================================*/
  type Effect = { effectType: EffectType; valueBps?: number; amountMinor?: number; currency?: Currency; bonusCredits?: number };
  const promoDefs: {
    key: string; name: string; desc: string; stacking: StackingMode; priority: number;
    start: number; end: number; active: boolean;
    rules: { type: RuleType; config: any }[];
    effect: Effect;
    coupon?: { code: string; maxRedemptions?: number; perUserLimit?: number; active?: boolean };
    budgets?: { currency: Currency; capMinor: number }[];
  }[] = [
    {
      key: "flash25", name: "Flash Sale — 25% Off", desc: "Headline flash-sale promo", stacking: "EXCLUSIVE", priority: 100,
      start: -2, end: 12, active: true,
      rules: [
        { type: "MIN_ORDER_AMOUNT", config: { amountMinor: 50000, currency: "INR" } },
        { type: "DATE_WINDOW", config: { startsAt: at(-2).toISOString(), endsAt: at(12).toISOString() } },
      ],
      effect: { effectType: "PERCENT_BPS", valueBps: 2500 },
      coupon: { code: "FLASH25", maxRedemptions: 5000, perUserLimit: 1 },
      budgets: [{ currency: "INR", capMinor: 50_000_000 }], // ₹5,00,000
    },
    {
      key: "first200", name: "First Purchase ₹200 Off", desc: "Auto-promo for first-time buyers", stacking: "STACK_WITH_COUPONS", priority: 50,
      start: -30, end: 60, active: true,
      rules: [{ type: "FIRST_PURCHASE", config: {} }],
      effect: { effectType: "FLAT_AMOUNT", amountMinor: 20000, currency: "INR" },
      budgets: [{ currency: "INR", capMinor: 20_000_000 }],
    },
    {
      key: "welcome", name: "Welcome 100 Bonus Credits", desc: "Bonus credits, stacks with anything", stacking: "STACK_ALL", priority: 10,
      start: -30, end: 90, active: true,
      rules: [{ type: "FIRST_PURCHASE", config: {} }],
      effect: { effectType: "BONUS_CREDITS", bonusCredits: 100 },
      // no money budget — bonus credits aren't a currency spend (HasBudget=false path)
    },
    {
      key: "upgrade15", name: "Pro Upgrade — 15% Off", desc: "Coupon, only on bigger packs", stacking: "STACK_PROMOS", priority: 30,
      start: -10, end: 40, active: true,
      rules: [{ type: "PLAN_IN", config: { planKeys: ["pro", "studio", "scale"] } }],
      effect: { effectType: "PERCENT_BPS", valueBps: 1500 },
      coupon: { code: "UPGRADE15", maxRedemptions: 1000, perUserLimit: 2 },
      budgets: [{ currency: "INR", capMinor: 30_000_000 }, { currency: "USD", capMinor: 500_00 }],
    },
    {
      key: "vip10", name: "VIP Segment $10 Off", desc: "Flat $10 for the VIP segment", stacking: "STACK_WITH_COUPONS", priority: 40,
      start: -20, end: 50, active: true,
      rules: [{ type: "USER_SEGMENT", config: { segment: "VIP" } }],
      effect: { effectType: "FLAT_AMOUNT", amountMinor: 1000, currency: "USD" },
      budgets: [{ currency: "USD", capMinor: 300_00 }],
    },
    {
      key: "save5", name: "Global SAVE5 — 5% Off", desc: "Capped at 500 global uses", stacking: "STACK_ALL", priority: 5,
      start: -15, end: 30, active: true,
      rules: [{ type: "MAX_USES_GLOBAL", config: { count: 500 } }],
      effect: { effectType: "PERCENT_BPS", valueBps: 500 },
      coupon: { code: "SAVE5", maxRedemptions: 500, perUserLimit: 1 },
      budgets: [{ currency: "INR", capMinor: 10_000_000 }, { currency: "USD", capMinor: 200_00 }],
    },
    {
      key: "diwali30", name: "Diwali 2024 — 30% Off", desc: "Expired campaign (kept for history)", stacking: "EXCLUSIVE", priority: 80,
      start: -240, end: -210, active: false,
      rules: [{ type: "DATE_WINDOW", config: { startsAt: at(-240).toISOString(), endsAt: at(-210).toISOString() } }],
      effect: { effectType: "PERCENT_BPS", valueBps: 3000 },
      coupon: { code: "DIWALI30", maxRedemptions: 2000, perUserLimit: 1, active: false },
    },

    /* -------- CHECKOUT EDGE PROMOS -------------------------------------------
     * mega60     — proves the Plans.maxDiscountBps clamp actually bites:
     *              6000bps on "scale" (cap 5000) clamps 60% -> 50%. (studio cap
     *              6000 = no bite; pro cap 7000 = no bite. scale is the case.)
     * budgetedge — 10% off with an almost-exhausted INR budget; section 8.5
     *              writes a CONSUMED spend ~₹10 from the cap, so the Redis DECRBY
     *              gate trips ErrExhausted and the rejected-order Release path runs.
     * couponnear — coupon NEARCAP at 4/5 + MAX_USES_GLOBAL count 5 at the brink.
     * couponmax  — coupon MAXEDOUT already at its cap (3/3) -> checkout rejects.
     * ----------------------------------------------------------------------*/
    {
      key: "mega60", name: "Mega 60% (clamp test)", desc: "60% off — exists to prove the maxDiscountBps clamp bites on low-cap plans", stacking: "STACK_PROMOS", priority: 60,
      start: -3, end: 30, active: true,
      rules: [{ type: "PLAN_IN", config: { planKeys: ["pro", "studio", "scale"] } }],
      effect: { effectType: "PERCENT_BPS", valueBps: 6000 },
      coupon: { code: "MEGA60", maxRedemptions: 1000, perUserLimit: 2 },
      budgets: [{ currency: "INR", capMinor: 50_000_000 }, { currency: "USD", capMinor: 500_00 }],
    },
    {
      key: "budgetedge", name: "Budget Edge 10% (gate test)", desc: "10% off with an almost-exhausted INR budget — drives DECRBY ErrExhausted / race", stacking: "STACK_ALL", priority: 12,
      start: -5, end: 30, active: true,
      rules: [], // no eligibility gate — we want to hit the BUDGET gate, nothing else
      effect: { effectType: "PERCENT_BPS", valueBps: 1000 },
      coupon: { code: "EDGE", maxRedemptions: 5000, perUserLimit: 5 },
      budgets: [{ currency: "INR", capMinor: 10_000_000 }], // ₹1,00,000 cap; ~₹10 left after 8.5
    },
    {
      key: "couponnear", name: "Near-Cap Coupon 5% (NEARCAP)", desc: "1 redemption left + MAX_USES_GLOBAL at the brink", stacking: "STACK_ALL", priority: 6,
      start: -5, end: 30, active: true,
      rules: [{ type: "MAX_USES_GLOBAL", config: { count: 5 } }],
      effect: { effectType: "PERCENT_BPS", valueBps: 500 },
      coupon: { code: "NEARCAP", maxRedemptions: 5, perUserLimit: 1 }, // 8.5 burns 4 -> one slot
    },
    {
      key: "couponmax", name: "Maxed Coupon 5% (MAXEDOUT)", desc: "Already at its redemption cap — checkout must reject", stacking: "STACK_ALL", priority: 6,
      start: -5, end: 30, active: true,
      rules: [],
      effect: { effectType: "PERCENT_BPS", valueBps: 500 },
      coupon: { code: "MAXEDOUT", maxRedemptions: 3, perUserLimit: 1 }, // 8.5 burns all 3
    },
  ];

  const promoByKey: Record<string, { id: string; effect: Effect }> = {};
  const couponByCode: Record<string, { id: string; promotionId: string }> = {};

  for (const pr of promoDefs) {
    const promo = await prisma.promotions.create({
      data: {
        name: pr.name,
        description: pr.desc,
        stackingMode: pr.stacking,
        priority: pr.priority,
        startsAt: at(pr.start),
        endsAt: at(pr.end),
        isActive: pr.active,
      },
    });
    promoByKey[pr.key] = { id: promo.id, effect: pr.effect };

    for (const r of pr.rules) {
      // resolve plan keys -> ids for PLAN_IN
      let config = r.config;
      if (r.type === "PLAN_IN" && Array.isArray(config.planKeys)) {
        config = { planIds: config.planKeys.map((k: string) => planByKey[k]!.id) };
      }
      await prisma.promotionRules.create({ data: { promotionId: promo.id, ruleType: r.type, config } });
    }

    await prisma.promotionEffects.create({
      data: {
        promotionId: promo.id,
        effectType: pr.effect.effectType,
        valueBps: pr.effect.valueBps ?? null,
        amountMinor: pr.effect.amountMinor != null ? B(pr.effect.amountMinor) : null,
        currency: pr.effect.currency ?? null,
        bonusCredits: pr.effect.bonusCredits ?? null,
      },
    });

    if (pr.coupon) {
      const c = await prisma.couponCode.create({
        data: {
          promotionId: promo.id,
          code: pr.coupon.code,
          maxRedemptions: pr.coupon.maxRedemptions ?? null,
          perUserLimit: pr.coupon.perUserLimit ?? 1,
          isActive: pr.coupon.active ?? true,
        },
      });
      couponByCode[pr.coupon.code] = { id: c.id, promotionId: promo.id };
    }

    for (const b of pr.budgets ?? []) {
      await prisma.promotionBudget.create({
        data: { promotionId: promo.id, currency: b.currency, capMinor: B(b.capMinor), spentMinorCached: B(0) },
      });
    }
  }

  /* ==========================================================================
   * 6. BANK OFFERS  (instrument-based) + BIN RANGES
   *    (+ "inmerch": merchant-funded offer pushed near its budgetCap in 8.5)
   * ========================================================================*/
  const bankDefs: {
    key: string; name: string; type: BankOfferType; funding: BankOfferFunding; bankCode: string | null;
    instruments: InstrumentType[]; networks: CardNetwork[]; country: string; currency: Currency;
    percentBps?: number; flatAmountMinor?: number; maxDiscountMinor?: number; minOrderMinor?: number;
    budgetCapMinor?: number; gateway: Gateway | null; gatewayOfferId: string | null;
    binMasked: string; start: number; end: number; active: boolean;
  }[] = [
    {
      key: "hdfc10", name: "10% off on HDFC Credit Cards", type: "INSTANT_DISCOUNT", funding: "BANK", bankCode: "HDFC",
      instruments: ["CREDIT_CARD"], networks: ["VISA", "MASTERCARD"], country: "IN", currency: "INR",
      percentBps: 1000, maxDiscountMinor: 150000, minOrderMinor: 100000, gateway: "RAZORPAY", gatewayOfferId: "offer_HDFC10",
      binMasked: "401234XXXXXX1234", start: -10, end: 30, active: true,
    },
    {
      key: "icici250", name: "₹250 off on ICICI Debit Cards", type: "INSTANT_DISCOUNT", funding: "BANK", bankCode: "ICICI",
      instruments: ["DEBIT_CARD"], networks: ["MASTERCARD"], country: "IN", currency: "INR",
      flatAmountMinor: 25000, minOrderMinor: 200000, gateway: "RAZORPAY", gatewayOfferId: "offer_ICICI250",
      binMasked: "521234XXXXXX5678", start: -10, end: 30, active: true,
    },
    {
      key: "axis5", name: "5% Cashback on Axis Cards", type: "CASHBACK", funding: "BANK", bankCode: "AXIS",
      instruments: ["CREDIT_CARD", "DEBIT_CARD"], networks: ["VISA"], country: "IN", currency: "INR",
      percentBps: 500, maxDiscountMinor: 50000, gateway: "RAZORPAY", gatewayOfferId: "offer_AXIS5CB",
      binMasked: "434073XXXXXX9012", start: -10, end: 30, active: true,
    },
    {
      key: "sbiemi", name: "No Cost EMI on SBI Cards", type: "NO_COST_EMI", funding: "SHARED", bankCode: "SBI",
      instruments: ["CREDIT_EMI", "DEBIT_EMI"], networks: ["RUPAY"], country: "IN", currency: "INR",
      percentBps: 0, gateway: "RAZORPAY", gatewayOfferId: "offer_SBIEMI",
      binMasked: "508500XXXXXX3456", start: -10, end: 30, active: true,
    },
    {
      key: "amex8", name: "8% Instant on Amex (merchant funded)", type: "INSTANT_DISCOUNT", funding: "MERCHANT", bankCode: "AMEX",
      instruments: ["CREDIT_CARD"], networks: ["AMEX"], country: "IN", currency: "INR",
      percentBps: 800, maxDiscountMinor: 200000, minOrderMinor: 300000, budgetCapMinor: 5_000_000,
      gateway: "RAZORPAY", gatewayOfferId: "offer_AMEX8", binMasked: "377412XXXXXX7890", start: -10, end: 30, active: true,
    },
    {
      key: "usvisa5", name: "5% off on Visa (US, merchant funded)", type: "INSTANT_DISCOUNT", funding: "MERCHANT", bankCode: null,
      instruments: ["CREDIT_CARD"], networks: ["VISA"], country: "US", currency: "USD",
      percentBps: 500, maxDiscountMinor: 2000, budgetCapMinor: 100_00,
      gateway: "STRIPE", gatewayOfferId: "coupon_us_visa5", binMasked: "411111XXXXXX1111", start: -10, end: 30, active: true,
    },
    {
      key: "dinersExpired", name: "Diners EMI Discount (ended)", type: "EMI_DISCOUNT", funding: "BANK", bankCode: "HDFC",
      instruments: ["CREDIT_EMI"], networks: ["DINERS"], country: "IN", currency: "INR",
      percentBps: 700, maxDiscountMinor: 100000, gateway: "RAZORPAY", gatewayOfferId: "offer_DINERS_OLD",
      binMasked: "360000XXXXXX2222", start: -120, end: -90, active: false,
    },

    /* -------- CHECKOUT EDGE BANK OFFER ---------------------------------------
     * inmerch — merchant-funded, dedicated bankCode "MFUND" (only BIN 455501
     * matches it) with a small budgetCap. 8.5 consumes ONE ₹2,000 order against
     * it, so spentMinorCached reconciles to ~₹500 below cap: the next order tips
     * it over, exercising the merchant-budget exhaustion path in isolation.
     * ----------------------------------------------------------------------*/
    {
      key: "inmerch", name: "Merchant-Funded ₹2,000 off (budget-edge)", type: "INSTANT_DISCOUNT", funding: "MERCHANT", bankCode: "MFUND",
      instruments: ["CREDIT_CARD"], networks: ["VISA"], country: "IN", currency: "INR",
      flatAmountMinor: 200000, budgetCapMinor: 250000,
      gateway: "RAZORPAY", gatewayOfferId: "offer_MFUND_EDGE", binMasked: "455501XXXXXX0000", start: -10, end: 30, active: true,
    },
  ];

  const bankByKey: Record<string, any> = {};
  for (const o of bankDefs) {
    const created = await prisma.bankOffer.create({
      data: {
        name: o.name, description: o.name, type: o.type, funding: o.funding, bankCode: o.bankCode,
        instruments: o.instruments, networks: o.networks, country: o.country, currency: o.currency,
        percentBps: o.percentBps ?? null,
        flatAmountMinor: o.flatAmountMinor != null ? B(o.flatAmountMinor) : null,
        maxDiscountMinor: o.maxDiscountMinor != null ? B(o.maxDiscountMinor) : null,
        minOrderMinor: o.minOrderMinor != null ? B(o.minOrderMinor) : null,
        budgetCapMinor: o.budgetCapMinor != null ? B(o.budgetCapMinor) : null,
        spentMinorCached: B(0),
        gateway: o.gateway, gatewayOfferId: o.gatewayOfferId,
        startsAt: at(o.start), endsAt: at(o.end), isActive: o.active,
      },
    });
    bankByKey[o.key] = {
      id: created.id, type: o.type, funding: o.funding, percentBps: o.percentBps ?? 0,
      flatAmountMinor: o.flatAmountMinor ?? 0, maxDiscountMinor: o.maxDiscountMinor ?? 0,
      gatewayOfferId: o.gatewayOfferId, binMasked: o.binMasked,
    };
  }

  await prisma.binRange.createMany({
    data: [
      { binPrefix: "401234", bankCode: "HDFC", network: "VISA", cardType: "CREDIT_CARD", cardLevel: "PLATINUM", country: "IN" },
      { binPrefix: "521234", bankCode: "ICICI", network: "MASTERCARD", cardType: "DEBIT_CARD", cardLevel: "STANDARD", country: "IN" },
      { binPrefix: "434073", bankCode: "AXIS", network: "VISA", cardType: "CREDIT_CARD", cardLevel: "SIGNATURE", country: "IN" },
      { binPrefix: "508500", bankCode: "SBI", network: "RUPAY", cardType: "DEBIT_EMI", cardLevel: "STANDARD", country: "IN" },
      { binPrefix: "377412", bankCode: "AMEX", network: "AMEX", cardType: "CREDIT_CARD", cardLevel: "GOLD", country: "IN" },
      { binPrefix: "411111", bankCode: "TESTUS", network: "VISA", cardType: "CREDIT_CARD", cardLevel: "STANDARD", country: "US" },
      { binPrefix: "55555555", bankCode: "TESTUS", network: "MASTERCARD", cardType: "CREDIT_CARD", cardLevel: "WORLD", country: "US" },
      { binPrefix: "360000", bankCode: "HDFC", network: "DINERS", cardType: "CREDIT_EMI", cardLevel: "BLACK", country: "IN", isActive: false },
      // ---- CHECKOUT EDGE BINs ----
      // 455501 -> the ONLY BIN that matches the merchant-funded "inmerch" offer.
      { binPrefix: "455501", bankCode: "MFUND", network: "VISA", cardType: "CREDIT_CARD", cardLevel: "STANDARD", country: "IN" },
      // 360001 -> ACTIVE DINERS card whose only matching offer (dinersExpired) is
      //           inactive: "recognised card, but the offer has ended" reject path.
      { binPrefix: "360001", bankCode: "HDFC", network: "DINERS", cardType: "CREDIT_EMI", cardLevel: "BLACK", country: "IN" },
    ],
  });

  /* ==========================================================================
   * 7. ORDERS  — the heart of it.  One helper, many scenarios.
   * ========================================================================*/

  // running state for caches / snapshots
  const balances = new Map<string, number>();
  const addLedger = async (
    userId: string, delta: number, reason: CreditReason, refType: LedgerReferenceType, refId: string
  ) => {
    const next = (balances.get(userId) ?? 0) + delta;
    balances.set(userId, next);
    await prisma.creditsLedger.create({
      data: { userId, delta, reason, referenceType: refType, referenceId: refId, balanceAfter: next },
    });
  };

  let invoiceSeq = 0;
  const invoiceSeries = "FY26";
  const orderCount = { total: 0 };

  type OrderOpts = {
    userIdx: number; planKey: string; currency: Currency; country?: string; city?: string;
    status: OrderStatus; gateway: Gateway; method?: string;
    promoKey?: string; couponCode?: string; bankKey?: string;
    refund?: "full" | "partial"; disputeStatus?: DisputeStatus;
    invoice?: boolean; placeOfSupply?: string; daysAgo?: number;
  };

  const makeOrder = async (o: OrderOpts) => {
    const user = users[o.userIdx]!;
    const plan = planByKey[o.planKey]!;
    const country = o.country ?? MARKETS.find((m) => m.currency === o.currency)!.country;
    const base = resolvePrice(plan.id, o.currency, country, o.city ?? "");

    const paid = ["PAID", "REFUNDED", "PARTIALLY_REFUNDED", "DISPUTED"].includes(o.status);
    const released = ["FAILED", "EXPIRED", "CANCELLED"].includes(o.status);

    // ---- promo discount ----
    let discountMinor = 0, bonusCredits = 0, promoKind: EffectType | null = null, promoId: string | null = null, couponId: string | null = null;
    if (o.promoKey) {
      const pr = promoByKey[o.promoKey]!;
      promoId = pr.id;
      promoKind = pr.effect.effectType;
      if (pr.effect.effectType === "PERCENT_BPS") discountMinor = Math.round((base * (pr.effect.valueBps ?? 0)) / 10000);
      else if (pr.effect.effectType === "FLAT_AMOUNT") discountMinor = pr.effect.currency === o.currency ? (pr.effect.amountMinor ?? 0) : 0;
      else if (pr.effect.effectType === "BONUS_CREDITS") bonusCredits = pr.effect.bonusCredits ?? 0;
      if (o.couponCode) couponId = couponByCode[o.couponCode]?.id ?? null;
    }
    // clamp to base and to the plan's max-discount margin cap
    const marginCap = Math.round((base * plan.maxBps) / 10000);
    discountMinor = Math.min(discountMinor, base, marginCap);

    // ---- bank offer (instant reduces the charge; cashback / no-cost-emi do not) ----
    let bankDiscountMinor = 0, cashbackMinor = 0, bankOfferId: string | null = null;
    let bankType: BankOfferType | null = null, bankGatewayOfferId: string | null = null, binMasked: string | null = null;
    let bankFunding: string | null = null;
    if (o.bankKey) {
      const bo = bankByKey[o.bankKey];
      bankOfferId = bo.id; bankType = bo.type; bankGatewayOfferId = bo.gatewayOfferId; binMasked = bo.binMasked; bankFunding = bo.funding;
      const afterPromo = base - discountMinor;
      let d = bo.percentBps ? Math.round((afterPromo * bo.percentBps) / 10000) : Number(bo.flatAmountMinor) || 0;
      if (bo.maxDiscountMinor) d = Math.min(d, Number(bo.maxDiscountMinor));
      if (bo.type === "INSTANT_DISCOUNT" || bo.type === "EMI_DISCOUNT") bankDiscountMinor = d;
      else if (bo.type === "CASHBACK") cashbackMinor = d; // promised, not deducted now
      // NO_COST_EMI: interest absorbed -> no charge change
    }

    // ---- tax (18% GST on INR orders) ----
    const taxable = base - discountMinor - bankDiscountMinor;
    let taxAmountMinor = 0, cgst = 0, sgst = 0, igst = 0;
    if (o.currency === "INR") {
      taxAmountMinor = Math.round(taxable * 0.18);
      if ((o.placeOfSupply ?? SELLER_STATE) === SELLER_STATE) {
        cgst = Math.round(taxAmountMinor / 2);
        sgst = taxAmountMinor - cgst;
      } else {
        igst = taxAmountMinor;
      }
    }
    const finalMinor = taxable + taxAmountMinor;
    const creditsGranted = plan.credits + bonusCredits;

    // ---- timestamps / gateway ids ----
    const createdAt = at(-(o.daysAgo ?? randInt(1, 60)));
    const expiresAt = new Date(createdAt.getTime() + 30 * 60 * 1000);
    const paidAt = paid ? new Date(createdAt.getTime() + randInt(1, 20) * 60 * 1000) : null;
    const gwOrderId = o.status === "CREATED" ? null : sid(`${o.gateway.toLowerCase()}_order_`);
    const traceId = sid("trace_");

    // ---- the order ----
    const order = await prisma.order.create({
      data: {
        idempotencyKey: sid(`idem_${o.userIdx}_`),
        userId: user.id,
        planId: plan.id,
        status: o.status,
        currency: o.currency,
        baseAmountMinor: B(base),
        discountAmountMinor: B(discountMinor),
        bankDiscountMinor: B(bankDiscountMinor),
        taxAmountMinor: B(taxAmountMinor),
        finalAmountMinor: B(finalMinor),
        creditsGranted,
        gateway: o.status === "CREATED" ? null : o.gateway,
        gatewayOrderId: gwOrderId,
        traceId,
        expiresAt,
        paidAt: paidAt ?? undefined,
        createdAt,
      },
    });
    orderCount.total++;

    // ---- payments ----
    const payStatus =
      o.status === "PAID" || o.status === "REFUNDED" || o.status === "PARTIALLY_REFUNDED" || o.status === "DISPUTED" ? "CAPTURED" :
      o.status === "AUTHORIZED" ? "AUTHORIZED" :
      o.status === "PENDING_PAYMENT" ? "REQUIRES_ACTION" :
      o.status === "CREATED" ? "CREATED" :
      "FAILED";

    // sometimes a paid order had a failed first attempt (retry story)
    if (paid && rand() < 0.3) {
      await prisma.payment.create({
        data: {
          orderId: order.id, gateway: o.gateway, gatewayPaymentId: sid("pay_"),
          amountMinor: B(finalMinor), currency: o.currency, status: "FAILED",
          method: o.method ?? "card", failureReason: "insufficient_funds",
          traceId, createdAt: new Date(createdAt.getTime() + 60_000),
        },
      });
    }

    const payment = await prisma.payment.create({
      data: {
        orderId: order.id,
        gateway: o.gateway,
        gatewayPaymentId: o.status === "CREATED" ? null : sid("pay_"),
        gatewayOfferId: bankGatewayOfferId,
        amountMinor: B(finalMinor),
        currency: o.currency,
        status: payStatus,
        method: o.method ?? "card",
        failureReason: payStatus === "FAILED" ? "card_declined" : null,
        authorizedAt: paid || o.status === "AUTHORIZED" ? paidAt ?? new Date(createdAt.getTime() + 120_000) : null,
        capturedAt: paid && o.status !== "AUTHORIZED" ? paidAt : null,
        traceId,
        createdAt: new Date(createdAt.getTime() + 90_000),
      },
    });

    // ---- refund ----
    let refund: { id: string } | null = null;
    if (o.refund && (o.status === "REFUNDED" || o.status === "PARTIALLY_REFUNDED")) {
      const refAmt = o.refund === "full" ? finalMinor : Math.round(finalMinor * 0.4);
      refund = await prisma.refund.create({
        data: {
          paymentId: payment.id, orderId: order.id, gateway: o.gateway,
          gatewayRefundId: sid("rfnd_"), amountMinor: B(refAmt), currency: o.currency,
          status: "SUCCEEDED", reason: o.refund === "full" ? "customer_request" : "partial_goodwill",
          idempotencyKey: sid("rfnd_idem_"),
          createdAt: new Date((paidAt ?? createdAt).getTime() + DAY),
        },
      });
    }

    // ---- dispute ----
    let dispute: { id: string } | null = null;
    if (o.disputeStatus && o.status === "DISPUTED") {
      const resolved = ["WON", "LOST", "ACCEPTED", "CANCELLED"].includes(o.disputeStatus);
      dispute = await prisma.dispute.create({
        data: {
          paymentId: payment.id, orderId: order.id, gateway: o.gateway,
          gatewayDisputeId: sid("dp_"), status: o.disputeStatus, reasonCode: "fraudulent",
          amountMinor: B(finalMinor), currency: o.currency,
          evidenceDueBy: at(7),
          openedAt: new Date((paidAt ?? createdAt).getTime() + 2 * DAY),
          resolvedAt: resolved ? at(-1) : null,
        },
      });
    }

    // ---- order-level discount snapshot ----
    if (promoId) {
      await prisma.orderDiscount.create({
        data: {
          orderId: order.id, promotionId: promoId, couponId, kind: promoKind!,
          discountMinor: B(discountMinor), creditsGranted: bonusCredits,
        },
      });

      const usageStatus = paid ? "CONSUMED" : released ? "RELEASED" : "RESERVED";
      await prisma.promotionUsage.create({
        data: { promotionId: promoId, couponId, userId: user.id, orderId: order.id, status: usageStatus },
      });

      // signed spend ledger (only money effects)
      if (discountMinor > 0) {
        if (usageStatus === "RELEASED") {
          await prisma.promotionSpend.create({ data: { promotionId: promoId, currency: o.currency, amountMinor: B(discountMinor), status: "RESERVED", orderId: order.id } });
          await prisma.promotionSpend.create({ data: { promotionId: promoId, currency: o.currency, amountMinor: B(-discountMinor), status: "RELEASED", orderId: order.id } });
        } else {
          await prisma.promotionSpend.create({ data: { promotionId: promoId, currency: o.currency, amountMinor: B(discountMinor), status: usageStatus, orderId: order.id } });
        }
      }
    }

    // ---- bank offer applied ----
    if (bankOfferId) {
      const boStatus = paid ? "CONSUMED" : released ? "RELEASED" : "RESERVED";
      await prisma.orderBankOffer.create({
        data: {
          orderId: order.id, bankOfferId, type: bankType!, discountMinor: B(bankDiscountMinor),
          cashbackMinor: B(cashbackMinor), binMasked, status: boStatus, gatewayOfferId: bankGatewayOfferId,
          reimbursed: bankFunding === "BANK" && paid ? rand() < 0.5 : false,
        },
      });
    }

    // ---- invoice (PAID, gapless number per series; skip fully-refunded) ----
    if (o.invoice && paid && o.status !== "REFUNDED") {
      invoiceSeq++;
      await prisma.invoice.create({
        data: {
          orderId: order.id, series: invoiceSeries, invoiceNumber: invoiceSeq,
          gstin: o.currency === "INR" ? "29ABCDE1234F1Z5" : null,
          placeOfSupply: o.currency === "INR" ? (o.placeOfSupply ?? SELLER_STATE) : null,
          cgstMinor: B(cgst), sgstMinor: B(sgst), igstMinor: B(igst), totalTaxMinor: B(taxAmountMinor),
          totalMinor: B(finalMinor), currency: o.currency, status: "ISSUED",
          pdfUrl: `https://invoices.payrail.dev/${invoiceSeries}/${invoiceSeq}.pdf`,
          issuedAt: paidAt ?? createdAt,
        },
      });
    }

    // ---- credits ledger ----
    if (paid) {
      await addLedger(user.id, plan.credits, "PURCHASE", "ORDER", order.id);
      if (bonusCredits > 0) await addLedger(user.id, bonusCredits, "PROMO_BONUS", "ORDER", order.id);
    }
    if (refund) {
      const clawed = o.refund === "full" ? plan.credits : Math.round(plan.credits * 0.4);
      await addLedger(user.id, -clawed, "REFUND", "REFUND", refund.id);
    }
    if (dispute && (o.disputeStatus === "LOST" || o.disputeStatus === "ACCEPTED")) {
      await addLedger(user.id, -(plan.credits + bonusCredits), "CHARGEBACK", "DISPUTE", dispute.id);
    }

    // ---- email + webhook trail ----
    if (paid) {
      await prisma.emailLog.create({ data: { userId: user.id, toEmail: user.email, template: "ORDER_PAID", referenceType: "ORDER", referenceId: order.id, status: "SENT", sentAt: paidAt } });
      await prisma.webhookEvents.create({ data: { eventId: sid("evt_"), gateway: o.gateway, eventType: "payment.captured", status: "PROCESSED", payload: { orderId: order.id, amount: finalMinor, currency: o.currency }, signature: sid("sig_"), processedAt: paidAt, traceId } });
    }
    if (refund) {
      await prisma.emailLog.create({ data: { userId: user.id, toEmail: user.email, template: "REFUND_DONE", referenceType: "REFUND", referenceId: refund.id, status: "SENT", sentAt: at(-1) } });
      await prisma.webhookEvents.create({ data: { eventId: sid("evt_"), gateway: o.gateway, eventType: "refund.processed", status: "PROCESSED", payload: { refundId: refund.id, orderId: order.id }, processedAt: at(-1), traceId } });
    }
    if (dispute) {
      await prisma.emailLog.create({ data: { userId: user.id, toEmail: user.email, template: "DISPUTE_OPENED", referenceType: "DISPUTE", referenceId: dispute.id, status: "QUEUED" } });
      await prisma.webhookEvents.create({ data: { eventId: sid("evt_"), gateway: o.gateway, eventType: "dispute.created", status: "RECEIVED", payload: { disputeId: dispute.id, orderId: order.id }, traceId } });
    }
    if (o.status === "FAILED") {
      await prisma.webhookEvents.create({ data: { eventId: sid("evt_"), gateway: o.gateway, eventType: "payment.failed", status: "PROCESSED", payload: { orderId: order.id, reason: "card_declined" }, processedAt: createdAt, traceId } });
    }

    return order;
  };

  // --- 7a. hand-crafted scenarios: one per status, every combo of the system ---
  const scenarios: OrderOpts[] = [
    // full-combo paid: flash promo + HDFC bank offer + GST (intra-state) + invoice
    { userIdx: 2, planKey: "pro", currency: "INR", status: "PAID", gateway: "RAZORPAY", promoKey: "flash25", couponCode: "FLASH25", bankKey: "hdfc10", invoice: true, placeOfSupply: "29", method: "card", daysAgo: 8 },
    // first-purchase flat ₹200 + ICICI debit + IGST (inter-state) + invoice
    { userIdx: 3, planKey: "starter", currency: "INR", status: "PAID", gateway: "RAZORPAY", promoKey: "first200", bankKey: "icici250", invoice: true, placeOfSupply: "07", method: "card", daysAgo: 12 },
    // welcome bonus credits + Axis cashback (no charge change) + UPI + invoice
    { userIdx: 4, planKey: "creator", currency: "INR", status: "PAID", gateway: "CASHFREE", promoKey: "welcome", bankKey: "axis5", invoice: true, placeOfSupply: "29", method: "upi", daysAgo: 5 },
    // USD: UPGRADE15 coupon + US Visa merchant offer (no GST outside IN)
    { userIdx: 6, planKey: "scale", currency: "USD", status: "PAID", gateway: "STRIPE", promoKey: "upgrade15", couponCode: "UPGRADE15", bankKey: "usvisa5", method: "card", daysAgo: 9 },
    // USD: VIP flat $10 (user 0 is VIP)
    { userIdx: 0, planKey: "pro", currency: "USD", status: "PAID", gateway: "STRIPE", promoKey: "vip10", method: "card", daysAgo: 15 },
    // AUTHORIZED (funds held, not captured) + bank offer reserved
    { userIdx: 5, planKey: "studio", currency: "INR", status: "AUTHORIZED", gateway: "RAZORPAY", bankKey: "hdfc10", method: "card", daysAgo: 0 },
    // PENDING_PAYMENT (3DS/OTP in flight) + SAVE5 coupon
    { userIdx: 13, planKey: "starter", currency: "INR", status: "PENDING_PAYMENT", gateway: "RAZORPAY", promoKey: "save5", couponCode: "SAVE5", method: "upi", daysAgo: 0 },
    // CREATED (no gateway order yet)
    { userIdx: 7, planKey: "lite", currency: "USD", status: "CREATED", gateway: "STRIPE", method: "card", daysAgo: 0 },
    // FAILED: promo + bank both RELEASED, spend nets to 0
    { userIdx: 2, planKey: "pro", currency: "INR", status: "FAILED", gateway: "RAZORPAY", promoKey: "flash25", couponCode: "FLASH25", bankKey: "hdfc10", method: "card", daysAgo: 3 },
    // EXPIRED (abandoned, swept)
    { userIdx: 4, planKey: "creator", currency: "INR", status: "EXPIRED", gateway: "CASHFREE", method: "upi", daysAgo: 20 },
    // CANCELLED
    { userIdx: 7, planKey: "studio", currency: "USD", status: "CANCELLED", gateway: "STRIPE", method: "card", daysAgo: 6 },
    // REFUNDED (full): PURCHASE then REFUND in the ledger
    { userIdx: 3, planKey: "pro", currency: "INR", status: "REFUNDED", gateway: "RAZORPAY", refund: "full", placeOfSupply: "29", method: "card", daysAgo: 18 },
    // PARTIALLY_REFUNDED (40%): invoice stays ISSUED
    { userIdx: 6, planKey: "scale", currency: "USD", status: "PARTIALLY_REFUNDED", gateway: "STRIPE", refund: "partial", method: "card", daysAgo: 22 },
    // DISPUTED + LOST -> CHARGEBACK claws credits back
    { userIdx: 5, planKey: "studio", currency: "INR", status: "DISPUTED", gateway: "RAZORPAY", disputeStatus: "LOST", invoice: true, placeOfSupply: "29", method: "card", daysAgo: 25 },
    // DISPUTED + WON -> credits retained
    { userIdx: 12, planKey: "pro", currency: "USD", status: "DISPUTED", gateway: "STRIPE", disputeStatus: "WON", method: "card", daysAgo: 27 },
    // other currencies
    { userIdx: 8, planKey: "creator", currency: "GBP", status: "PAID", gateway: "PAYPAL", method: "paypal", daysAgo: 14 },
    { userIdx: 9, planKey: "studio", currency: "EUR", status: "PAID", gateway: "STRIPE", method: "card", daysAgo: 11 },
    { userIdx: 11, planKey: "pro", currency: "AED", status: "PAID", gateway: "PAYPAL", method: "card", daysAgo: 7 },
    { userIdx: 10, planKey: "starter", currency: "SGD", status: "PAID", gateway: "STRIPE", method: "card", daysAgo: 4 },
    // city-level price override (Bengaluru beats IN default) + invoice
    { userIdx: 1, planKey: "pro", currency: "INR", country: "IN", city: "Bengaluru", status: "PAID", gateway: "RAZORPAY", invoice: true, placeOfSupply: "29", method: "card", daysAgo: 2 },
  ];
  for (const s of scenarios) await makeOrder(s);

  // --- 7b. bulk volume: ~32 more orders, mostly PAID w/ invoices, some refunds/disputes ---
  const bulkPlans = ["lite", "starter", "creator", "pro", "studio", "scale"];
  const percentPromos = ["flash25", "save5"]; // currency-agnostic, safe to attach anywhere
  for (let i = 0; i < 32; i++) {
    const market = choice(MARKETS);
    const gateway = choice(market.gateways);
    const planKey = choice(bulkPlans);
    // NB: exclude the fresh users at the tail — they must stay order-free
    const userIdx = randInt(0, users.length - 1 - NUM_FRESH_USERS);

    // status mix: ~70% PAID, then refunds/disputes/failures
    const roll = rand();
    let status: OrderStatus = "PAID";
    let refund: "full" | "partial" | undefined;
    let disputeStatus: DisputeStatus | undefined;
    if (roll > 0.94) { status = "DISPUTED"; disputeStatus = rand() < 0.5 ? "UNDER_REVIEW" : "LOST"; }
    else if (roll > 0.88) { status = "PARTIALLY_REFUNDED"; refund = "partial"; }
    else if (roll > 0.82) { status = "REFUNDED"; refund = "full"; }
    else if (roll > 0.76) { status = "FAILED"; }

    const wantPromo = rand() < 0.25 && market.currency === "INR";
    const wantBank = rand() < 0.3;
    const bankKey = wantBank
      ? (market.currency === "INR" ? choice(["hdfc10", "axis5", "amex8"]) : market.currency === "USD" ? "usvisa5" : undefined)
      : undefined;

    await makeOrder({
      userIdx,
      planKey,
      currency: market.currency,
      country: market.country,
      status,
      gateway,
      method: market.currency === "INR" && rand() < 0.4 ? "upi" : "card",
      promoKey: wantPromo ? choice(percentPromos) : undefined,
      couponCode: wantPromo ? (status !== "FAILED" ? "FLASH25" : undefined) : undefined,
      bankKey,
      refund,
      disputeStatus,
      invoice: market.currency === "INR" && status === "PAID",
      placeOfSupply: market.currency === "INR" ? choice(["29", "07", "27", "19"]) : undefined,
    });
  }

  /* ==========================================================================
   * 8. STANDALONE INFRA / OPS ROWS  (not tied to a single order)
   * ========================================================================*/

  // a couple of manual credit adjustments + an expiry sweep
  await addLedger(users[0]!.id, 250, "ADJUSTMENT", "ADJUSTMENT", sid("adj_")); // goodwill credit
  await addLedger(users[6]!.id, -120, "EXPIRY", "ADJUSTMENT", sid("exp_"));    // credits expired

  // webhook edge cases: a failed-with-retry one and a dead-lettered one
  await prisma.webhookEvents.create({
    data: { eventId: sid("evt_"), gateway: "RAZORPAY", eventType: "payment.captured", status: "FAILED", payload: { note: "handler threw" }, attempts: 3, lastError: "downstream timeout", nextRetryAt: at(0.02), traceId: sid("trace_") },
  });
  await prisma.webhookEvents.create({
    data: { eventId: sid("evt_"), gateway: "STRIPE", eventType: "charge.refunded", status: "DEAD_LETTER", payload: { note: "retries exhausted" }, attempts: 8, lastError: "schema mismatch", traceId: sid("trace_") },
  });
  await prisma.webhookEvents.create({
    data: { eventId: sid("evt_"), gateway: "PAYPAL", eventType: "checkout.order.approved", status: "RECEIVED", payload: { note: "fresh, not yet processed" }, traceId: sid("trace_") },
  });

  // idempotency replay cache (checkout). status DONE = a completed, replayable response.
  await prisma.idempotencyRecord.createMany({
    data: [
      { idempotencyKey: sid("ik_"), userId: users[2]!.id, endpoint: "POST /checkout", requestHash: sid("hash_"), responseStatus: 200, responseBody: { orderId: "replayed-1", status: "CREATED" }, status: "DONE", expiresAt: at(1) },
      { idempotencyKey: sid("ik_"), userId: users[6]!.id, endpoint: "POST /checkout", requestHash: sid("hash_"), responseStatus: 200, responseBody: { orderId: "replayed-2", status: "PENDING_PAYMENT" }, status: "DONE", expiresAt: at(1) },
      { idempotencyKey: sid("ik_"), userId: users[3]!.id, endpoint: "POST /checkout/confirm", requestHash: sid("hash_"), responseStatus: 409, responseBody: { error: "already_captured" }, status: "DONE", expiresAt: at(0.5) },
    ],
  });

  // poison-message parking for each async source
  await prisma.deadLetterEvent.createMany({
    data: [
      { source: "ORDER_INTENT", partitionKey: users[2]!.id, payload: { kind: "order.intent", reason: "plan not found" }, error: "PlanNotFound", attempts: 5 },
      { source: "SETTLEMENT", partitionKey: "razorpay_order_x", payload: { kind: "settle", reason: "ledger write conflict" }, error: "SerializationFailure", attempts: 6 },
      { source: "EMAIL", partitionKey: users[6]!.id, payload: { template: "ORDER_PAID", reason: "SMTP 550" }, error: "MailRejected", attempts: 4, resolvedAt: at(-0.5) },
      { source: "RECONCILE", partitionKey: "global", payload: { kind: "budget.reconcile", reason: "redis unreachable" }, error: "RedisTimeout", attempts: 3 },
    ],
  });

  // extra standalone emails (queue states)
  await prisma.emailLog.createMany({
    data: [
      { userId: users[8]!.id, toEmail: users[8]!.email, template: "WELCOME", referenceType: "USER", referenceId: users[8]!.id, status: "SENT", sentAt: at(-14) },
      { userId: users[10]!.id, toEmail: users[10]!.email, template: "PAYMENT_FAILED", referenceType: "USER", referenceId: users[10]!.id, status: "QUEUED" },
      { userId: users[11]!.id, toEmail: users[11]!.email, template: "CARD_EXPIRING", referenceType: "USER", referenceId: users[11]!.id, status: "FAILED" },
    ],
  });

  // a couple of admin audit entries (admin-api builds later, but exercise the table)
  await prisma.adminAuditLog.createMany({
    data: [
      { actorId: admins[0]!.id, action: "promotion.create", entityType: "Promotions", entityId: promoByKey["flash25"]!.id, before: undefined, after: { name: "Flash Sale — 25% Off", isActive: true }, ip: "10.0.0.4", userAgent: "payrail-admin/1.0" },
      { actorId: admins[1]!.id, action: "refund.issue", entityType: "Refund", entityId: "manual-1", before: { status: "PENDING" }, after: { status: "SUCCEEDED" }, ip: "10.0.0.5", userAgent: "payrail-admin/1.0" },
      { actorId: admins[1]!.id, action: "promotionBudget.update", entityType: "PromotionBudget", entityId: "bud-1", before: { capMinor: "50000000" }, after: { capMinor: "60000000" }, ip: "10.0.0.5", userAgent: "payrail-admin/1.0" },
    ],
  });

  /* ==========================================================================
   * 8.5  CHECKOUT-API EDGE FIXTURES  (deterministic, named, ledger-consistent)
   *      Runs BEFORE reconciliation so near-cap caches come from real ledgers.
   * ========================================================================*/
  const seedCheckoutFixtures = async () => {
    console.log("   ↳ checkout edge fixtures …");

    // (a) Active plan priced ONLY in INR. A USD/US checkout for it walks
    //     ResolvePricing -> country miss -> US fallback -> ErrNotFound. INR works.
    const inrOnlyPlan = await prisma.plans.create({
      data: {
        name: "INR-Only Pack", description: "Active but priced only in INR — exercises the ResolvePricing US-fallback miss",
        credits: 250, isActive: true, maxDiscountBps: 10000, sacCode: "998314",
      },
    });
    await prisma.planPrice.create({
      data: { planId: inrOnlyPlan.id, country: "IN", city: "", currency: "INR", amountMinor: B(fromUsd(700, "INR")), isActive: true },
    });

    // (b) Push the "budgetedge" promo's INR budget to ~₹10 from its ₹1,00,000 cap.
    //     One synthetic CONSUMED spend row; reconciler (9b) sums it -> near-cap.
    await prisma.promotionSpend.create({
      data: { promotionId: promoByKey["budgetedge"]!.id, currency: "INR", amountMinor: B(9_999_000), status: "CONSUMED" },
    });

    // (c) Burn 4 of NEARCAP's 5 redemptions across 4 DISTINCT users (so no single
    //     user is over perUserLimit — it's a global-cap edge, not a per-user one).
    const nearUsers = [users[7]!, users[8]!, users[9]!, users[10]!];
    for (const u of nearUsers) {
      await prisma.promotionUsage.create({
        data: { promotionId: promoByKey["couponnear"]!.id, couponId: couponByCode["NEARCAP"]!.id, userId: u.id, status: "CONSUMED" },
      });
    }

    // (d) Fully exhaust MAXEDOUT (3/3) across 3 users -> checkout MUST reject.
    const maxUsers = [users[11]!, users[12]!, users[13]!];
    for (const u of maxUsers) {
      await prisma.promotionUsage.create({
        data: { promotionId: promoByKey["couponmax"]!.id, couponId: couponByCode["MAXEDOUT"]!.id, userId: u.id, status: "CONSUMED" },
      });
    }

    // (e) One PAID order that consumes the merchant-funded "inmerch" offer (₹2,000).
    //     Reconciler (9d) sums CONSUMED instant discounts -> spent ₹2,000 of ₹2,500
    //     cap, leaving ~₹500: the very next inmerch order tips it over the cap.
    await makeOrder({
      userIdx: 6, planKey: "scale", currency: "INR", country: "IN",
      status: "PAID", gateway: "RAZORPAY", bankKey: "inmerch", method: "card", daysAgo: 3,
    });

    // (f) A live CREATED order with a FIXED idempotency key, so a checkout test can
    //     replay "checkout-collision-demo" for user 0 and hit @@unique(userId,key).
    const collisionPlan = planByKey["pro"]!;
    const collisionBase = resolvePrice(collisionPlan.id, "USD", "US");
    const collisionCreatedAt = at(0);
    await prisma.order.create({
      data: {
        idempotencyKey: "checkout-collision-demo",
        userId: users[0]!.id,
        planId: collisionPlan.id,
        status: "CREATED",
        currency: "USD",
        baseAmountMinor: B(collisionBase),
        discountAmountMinor: B(0),
        bankDiscountMinor: B(0),
        taxAmountMinor: B(0),
        finalAmountMinor: B(collisionBase),
        creditsGranted: collisionPlan.credits,
        gateway: null,
        gatewayOrderId: null,
        traceId: sid("trace_"),
        expiresAt: new Date(collisionCreatedAt.getTime() + 30 * 60 * 1000),
        createdAt: collisionCreatedAt,
      },
    });

    // (g) Claim-first IN_PROGRESS row: a concurrent retry of this key+endpoint must
    //     get 409 (winner still working), not start a second DECRBY. (The DONE rows
    //     in section 8 cover the happy replay path.)
    await prisma.idempotencyRecord.create({
      data: {
        idempotencyKey: "checkout-inflight-demo",
        userId: users[0]!.id,
        endpoint: "POST /v1/checkout",
        requestHash: sid("hash_"),
        status: "IN_PROGRESS", // no responseBody yet — the work hasn't finished
        expiresAt: at(1),
      },
    });
  };
  await seedCheckoutFixtures();

  /* ==========================================================================
   * 9. RECONCILE CACHES from the source-of-truth ledgers
   * ========================================================================*/

  // 9a. User.creditsBalance = SUM(CreditsLedger.delta)  (fresh users settle to 0)
  for (const u of users) {
    const agg = await prisma.creditsLedger.aggregate({ where: { userId: u.id }, _sum: { delta: true } });
    await prisma.user.update({ where: { id: u.id }, data: { creditsBalance: agg._sum.delta ?? 0 } });
  }

  // 9b. PromotionBudget.spentMinorCached = SUM(PromotionSpend.amountMinor)
  const budgets = await prisma.promotionBudget.findMany();
  for (const b of budgets) {
    const agg = await prisma.promotionSpend.aggregate({ where: { promotionId: b.promotionId, currency: b.currency }, _sum: { amountMinor: true } });
    await prisma.promotionBudget.update({ where: { id: b.id }, data: { spentMinorCached: agg._sum.amountMinor ?? BigInt(0) } });
  }

  // 9c. CouponCode.redeemedCount = COUNT(usages not released)
  const coupons = await prisma.couponCode.findMany();
  for (const c of coupons) {
    const cnt = await prisma.promotionUsage.count({ where: { couponId: c.id, status: { not: "RELEASED" } } });
    await prisma.couponCode.update({ where: { id: c.id }, data: { redeemedCount: cnt } });
  }

  // 9d. BankOffer.spentMinorCached (merchant-funded only) = SUM(consumed instant discounts)
  const merchantOffers = await prisma.bankOffer.findMany({ where: { funding: "MERCHANT" } });
  for (const o of merchantOffers) {
    const agg = await prisma.orderBankOffer.aggregate({ where: { bankOfferId: o.id, status: "CONSUMED" }, _sum: { discountMinor: true } });
    await prisma.bankOffer.update({ where: { id: o.id }, data: { spentMinorCached: agg._sum.discountMinor ?? BigInt(0) } });
  }

  // 9e. reconciliation log rows (what the reconciler would write)
  const flashBudget = budgets.find((b) => b.promotionId === promoByKey["flash25"]!.id && b.currency === "INR");
  if (flashBudget) {
    const spent = await prisma.promotionSpend.aggregate({ where: { promotionId: flashBudget.promotionId, currency: "INR" }, _sum: { amountMinor: true } });
    const ledgerSpent = spent._sum.amountMinor ?? BigInt(0);
    const redisRemaining = flashBudget.capMinor - ledgerSpent - BigInt(1200); // pretend Redis drifted by ₹12
    await prisma.reconciliationLog.create({
      data: { promotionId: flashBudget.promotionId, currency: "INR", redisRemaining, ledgerSpentMinor: ledgerSpent, driftMinor: BigInt(1200), corrected: true, note: "Redis reseeded from ledger (ledger wins)" },
    });
  }
  // and a drift row for the near-cap "budgetedge" promo (so the gate has a story)
  const edgeBudget = budgets.find((b) => b.promotionId === promoByKey["budgetedge"]!.id && b.currency === "INR");
  if (edgeBudget) {
    const spent = await prisma.promotionSpend.aggregate({ where: { promotionId: edgeBudget.promotionId, currency: "INR" }, _sum: { amountMinor: true } });
    const ledgerSpent = spent._sum.amountMinor ?? BigInt(0);
    await prisma.reconciliationLog.create({
      data: { promotionId: edgeBudget.promotionId, currency: "INR", redisRemaining: edgeBudget.capMinor - ledgerSpent, ledgerSpentMinor: ledgerSpent, driftMinor: BigInt(0), corrected: false, note: "near-cap budget — Redis in sync with ledger" },
    });
  }
  await prisma.reconciliationLog.create({
    data: { promotionId: null, currency: null, redisRemaining: null, ledgerSpentMinor: null, driftMinor: BigInt(0), corrected: false, note: "global nightly run — no drift" },
  });

  /* ==========================================================================
   * 10. SUMMARY
   * ========================================================================*/
  const counts = {
    users: await prisma.user.count(),
    plans: await prisma.plans.count(),
    planPrices: await prisma.planPrice.count(),
    promotions: await prisma.promotions.count(),
    coupons: await prisma.couponCode.count(),
    bankOffers: await prisma.bankOffer.count(),
    binRanges: await prisma.binRange.count(),
    orders: await prisma.order.count(),
    payments: await prisma.payment.count(),
    refunds: await prisma.refund.count(),
    disputes: await prisma.dispute.count(),
    orderDiscounts: await prisma.orderDiscount.count(),
    orderBankOffers: await prisma.orderBankOffer.count(),
    invoices: await prisma.invoice.count(),
    creditsLedger: await prisma.creditsLedger.count(),
    webhooks: await prisma.webhookEvents.count(),
    emails: await prisma.emailLog.count(),
    idempotency: await prisma.idempotencyRecord.count(),
    deadLetters: await prisma.deadLetterEvent.count(),
    reconciliations: await prisma.reconciliationLog.count(),
  };
  console.log("✅  Seed complete:");
  console.table(counts);

  // quick pointer to the named checkout handles (so tests know what to grab)
  console.log("🧪  Checkout edge handles: plans 'INR-Only Pack' · promos MEGA60/EDGE · coupons NEARCAP(4/5)/MAXEDOUT(3/3) · bank 'inmerch'(~₹500 left) · BIN 360001 · order 'checkout-collision-demo' · idemKey 'checkout-inflight-demo' (IN_PROGRESS) · fresh users 14/15");
}

main()
  .then(async () => {
    await prisma.$disconnect();
  })
  .catch(async (e) => {
    console.error("❌  Seed failed:", e);
    await prisma.$disconnect();
    process.exit(1);
  });

/* ============================================================================
 * PRISMA 7 NOTE (driver adapters)
 * ----------------------------------------------------------------------------
 * If running this throws "PrismaClient requires a driver adapter", you're on
 * Prisma 7. Replace the client import + instantiation at the top with:
 *
 *     import { PrismaClient } from "../generated/prisma/client";
 *     import { PrismaPg } from "@prisma/adapter-pg";
 *     const adapter = new PrismaPg({ connectionString: process.env.DATABASE_URL });
 *     const prisma = new PrismaClient({ adapter });
 *
 * and install the adapter:   npm i @prisma/adapter-pg pg
 * ============================================================================
 * SCHEMA ADDITION THIS SEED ASSUMES (implied by the HLD hardening):
 *
 *   // Claim-first idempotency (hardening §4) needs a status column:
 *   enum IdempotencyStatus {
 *     IN_PROGRESS
 *     DONE
 *     FAILED
 *   }
 *   model IdempotencyRecord {
 *     // …existing fields…
 *     status IdempotencyStatus @default(IN_PROGRESS)
 *   }
 *
 * NOTE: user segments ("VIP") are NOT a stored column — USER_SEGMENT rules are
 * evaluated by code logic at checkout (e.g. order velocity), so nothing to seed.
 *
 * After adding this:  npx prisma generate  (then run the seed).
 * ==========================================================================*/