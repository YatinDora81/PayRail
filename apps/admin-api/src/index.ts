import {prisma} from "@repo/db";

prisma.$connect().then(() => {
  console.log("Connected to the database");
}).catch((error) => {
  console.error("Error connecting to the database:", error);
});