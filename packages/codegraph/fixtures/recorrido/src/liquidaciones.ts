import axios from "axios";
import { Pool } from "pg";

const pool = new Pool();

export async function postLiquidaciones(peticion: { base: number; regimen: string }) {
  const contribuyente = await axios.get("https://padron.example/uno");

  if (peticion.regimen === "simplificado") {
    const plana = peticion.base * 0.05;
    await pool.query("insert into liquidaciones values ($1)", [plana]);
    return plana;
  }

  let total = 0;
  for (const tramo of [0.1, 0.2, 0.3]) {
    total += peticion.base * tramo;
  }
  if (!contribuyente.data.activo) {
    throw new Error("contribuyente inactivo");
  }
  await pool.query("insert into liquidaciones values ($1)", [total]);
  return total;
}
