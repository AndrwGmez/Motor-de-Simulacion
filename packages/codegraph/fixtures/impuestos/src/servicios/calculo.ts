import { guardarLiquidacion } from "../datos/repositorio";
import { consultarPadron } from "../externos/padron";

export async function calcularLiquidacion(base: number) {
  const contribuyente = await consultarPadron("123");
  const total = base * (contribuyente.exento ? 0 : 0.19);
  await guardarLiquidacion(total);
  return total;
}
