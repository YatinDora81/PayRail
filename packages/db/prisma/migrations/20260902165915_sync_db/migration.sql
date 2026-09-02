/*
  Warnings:

  - The values [STACK_PROMOS,STACK_WITH_COUPONS,STACK_ALL] on the enum `StackingMode` will be removed. If these variants are still used in the database, this will fail.
  - Made the column `promotionId` on table `CouponCode` required. This step will fail if there are existing NULL values in that column.

*/
-- AlterEnum
BEGIN;
CREATE TYPE "StackingMode_new" AS ENUM ('EXCLUSIVE', 'STACKABLE');
ALTER TABLE "public"."Promotions" ALTER COLUMN "stackingMode" DROP DEFAULT;
ALTER TABLE "Promotions" ALTER COLUMN "stackingMode" TYPE "StackingMode_new" USING ("stackingMode"::text::"StackingMode_new");
ALTER TYPE "StackingMode" RENAME TO "StackingMode_old";
ALTER TYPE "StackingMode_new" RENAME TO "StackingMode";
DROP TYPE "public"."StackingMode_old";
ALTER TABLE "Promotions" ALTER COLUMN "stackingMode" SET DEFAULT 'EXCLUSIVE';
COMMIT;

-- DropForeignKey
ALTER TABLE "CouponCode" DROP CONSTRAINT "CouponCode_promotionId_fkey";

-- AlterTable
ALTER TABLE "CouponCode" ALTER COLUMN "promotionId" SET NOT NULL;

-- CreateIndex
CREATE INDEX "Order_status_expiresAt_idx" ON "Order"("status", "expiresAt");

-- CreateIndex
CREATE INDEX "Order_gatewayOrderId_idx" ON "Order"("gatewayOrderId");

-- CreateIndex
CREATE INDEX IF NOT EXISTS "OutboxEvent_unpublished_idx" ON "OutboxEvent" ("createdAt") WHERE "publishedAt" IS NULL;

-- AddForeignKey
ALTER TABLE "CouponCode" ADD CONSTRAINT "CouponCode_promotionId_fkey" FOREIGN KEY ("promotionId") REFERENCES "Promotions"("id") ON DELETE RESTRICT ON UPDATE CASCADE;
