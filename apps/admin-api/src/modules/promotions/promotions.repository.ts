import { prisma, type Prisma } from "@repo/db";

class PromotionsRepository {
  listAll = () => {
    return prisma.promotions.findMany({
      orderBy: [{ priority: "desc" }, { createdAt: "desc" }],
      include: { effects: true, _count: { select: { coupons: true } } },
    });
  };
}

export default new PromotionsRepository();
