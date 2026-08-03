import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import type { ModeloCodigo } from "../model";
import type { Funcion, Llamada } from "./typescript-functions";

/**
 * Adaptador de Go.
 *
 * El análisis lo hace un programa en Go con `go/ast`, el analizador del propio
 * compilador. Reimplementar la sintaxis de Go en TypeScript habría sido
 * garantizar errores sutiles; delegarlo cuesta una llamada a proceso.
 */

export interface AnalisisGo {
  modelo: ModeloCodigo;
  funciones: Funcion[];
  llamadas: Llamada[];
}

export function hayGoDisponible(): boolean {
  try {
    execFileSync("go", ["version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}

export function analizarGo(raizAbsoluta: string): AnalisisGo {
  const raiz = resolve(raizAbsoluta);
  // El analizador vive junto al paquete, tanto en fuente como compilado.
  const candidatos = [
    resolve(process.cwd(), "go"),
    resolve(process.cwd(), "packages/codegraph/go"),
    resolve(new URL(".", import.meta.url).pathname, "../../go"),
  ];
  const analizador = candidatos.find((ruta) => existsSync(resolve(ruta, "main.go")));
  if (!analizador) throw new Error("Falta el analizador de Go en packages/codegraph/go.");

  const salida = execFileSync("go", ["run", ".", raiz], {
    cwd: analizador,
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
  });
  const bruto = JSON.parse(salida) as {
    modulos: ModeloCodigo["modulos"];
    referencias: ModeloCodigo["referencias"];
    funciones: Funcion[];
    llamadas: Llamada[];
  };

  return {
    modelo: { raiz, modulos: bruto.modulos ?? [], referencias: bruto.referencias ?? [] },
    funciones: bruto.funciones ?? [],
    llamadas: bruto.llamadas ?? [],
  };
}
