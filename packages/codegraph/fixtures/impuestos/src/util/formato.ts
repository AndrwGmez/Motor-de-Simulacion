export function formatearImporte(valor: number) {
  return new Intl.NumberFormat("es-CO").format(valor);
}
