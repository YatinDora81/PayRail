import { prisma } from "@repo/db";
import type { ListPromotionsInput } from "./promotions.schema";
import { cached } from "../../lib/cache";
import promotionsRepository from "./promotions.repository";
import { paginate, toSkipTake } from "../../lib/pagination";

class PromotionsService {
  adminListKey = "promotions:admin:list";

  list = async (query: ListPromotionsInput) => {
    const all = await cached(this.adminListKey, 300, () =>
      promotionsRepository.listAll(),
    );
    
    const q = query.q?.toLowerCase();
    const filtered = all.filter(
      (p) =>
        (query.isActive === undefined || p.isActive === query.isActive) &&
        (!q || p.name.toLowerCase().includes(q)),
    );

    const { skip, take } = toSkipTake(query);
    return paginate(filtered.slice(skip, skip + take), filtered.length, {
      page: query.page,
      limit: query.limit,
    });
  };
}

export default new PromotionsService();
