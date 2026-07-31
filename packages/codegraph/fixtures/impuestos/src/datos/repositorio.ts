import { Pool } from "pg";

const pool = new Pool();

export async function guardarLiquidacion(total: number) {
  await pool.query("insert into liquidaciones (total) values ($1)", [total]);
}
