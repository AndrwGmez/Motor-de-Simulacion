import { calcularLiquidacion } from "../servicios/calculo";
import { formatearImporte } from "../util/formato";

export async function postLiquidaciones(peticion: { base: number }) {
  return formatearImporte(await calcularLiquidacion(peticion.base));
}
