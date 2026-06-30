-- CreateEnum
CREATE TYPE "IdempotencyStatus" AS ENUM ('IN_PROGRESS', 'DONE', 'FAILED');

-- AlterTable
ALTER TABLE "IdempotencyRecord" ADD COLUMN     "status" "IdempotencyStatus" NOT NULL DEFAULT 'IN_PROGRESS';
