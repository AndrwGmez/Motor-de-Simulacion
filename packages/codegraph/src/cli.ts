#!/usr/bin/env node
import { writeFileSync } from "node:fs";
import { analizar, type Modo } from "./index";

/**
 * flowverse-codegraph <ruta> [--modo modules] [--salida archivo.json]
 *
 * Sin --salida escribe en la salida estándar, para poder encadenar con `>`.
 */
async function main(): Promise<void> {
  const args = process.argv.slice(2);
  const ruta = args.find((a) => !a.startsWith("--"));
  if (!ruta) {
    process.stderr.write("Uso: flowverse-codegraph <ruta> [--modo modules] [--salida archivo.json]\n");
    process.exit(2);
  }
  const valor = (nombre: string): string | undefined => {
    const indice = args.indexOf(`--${nombre}`);
    return indice >= 0 ? args[indice + 1] : undefined;
  };

  try {
    const flujo = await analizar(ruta, { modo: (valor("modo") ?? "modules") as Modo });
    const json = `${JSON.stringify(flujo, null, 2)}\n`;
    const salida = valor("salida");
    if (salida) {
      writeFileSync(salida, json);
      process.stderr.write(`${flujo.nodes.length} nodos · ${flujo.edges.length} conexiones → ${salida}\n`);
    } else {
      process.stdout.write(json);
      process.stderr.write(`${flujo.nodes.length} nodos · ${flujo.edges.length} conexiones\n`);
    }
  } catch (error) {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    process.exit(1);
  }
}

void main();
