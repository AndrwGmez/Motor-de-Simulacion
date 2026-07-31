/**
 * El contrato exige identificadores `^[A-Za-z][A-Za-z0-9_-]{0,63}$`, y las rutas
 * de archivo no lo cumplen casi nunca: llevan barras, puntos y suelen pasar de
 * 64 caracteres. Este saneador produce un identificador legible y estable, y
 * resuelve las colisiones que provoca el recorte con un sufijo derivado de la
 * ruta completa, no de un contador: así el mismo proyecto da siempre el mismo
 * grafo y las versiones se pueden comparar entre sí.
 */

const MAXIMO = 64;

function huella(valor: string): string {
  let hash = 2166136261;
  for (let i = 0; i < valor.length; i += 1) {
    hash ^= valor.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0).toString(36).slice(0, 6);
}

export function identificador(ruta: string, usados: Set<string>): string {
  let base = ruta
    .replace(/\.[jt]sx?$/, "")
    .replace(/[^A-Za-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "");
  if (!/^[A-Za-z]/.test(base)) base = `n_${base}`;

  let candidato = base.slice(0, MAXIMO);
  if (candidato !== base || usados.has(candidato)) {
    const sufijo = `_${huella(ruta)}`;
    candidato = `${base.slice(0, MAXIMO - sufijo.length)}${sufijo}`;
  }
  return candidato;
}
