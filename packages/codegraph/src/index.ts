import { basename, resolve } from "node:path";
import type { FlowDefinition } from "@flowverse/core";
import { analizarTypeScript } from "./adapters/typescript";
import { extraerModulos } from "./extractors/modules";
import { extraerRecorrido } from "./adapters/typescript-journey";
import { extraerRecorridoDeNegocio } from "./extractors/journey";
import { analizarFunciones } from "./adapters/typescript-functions";
import { extraerFunciones } from "./extractors/functions";
import { analizarGo, hayGoDisponible } from "./adapters/go";
import { readdirSync, statSync } from "node:fs";
import { join } from "node:path";

export type Modo = "modules" | "functions" | "journey";

export type Lenguaje = "typescript" | "go";

export interface OpcionesAnalisis {
  modo: Modo;
  /** Se detecta por el contenido si no se indica. */
  lenguaje?: Lenguaje;
  /** Topes del contrato. Se comprueban antes de emitir, no después. */
  limiteNodos?: number;
  /** Filtros de alcance: sin ellos un proyecto real produce miles de nodos. */
  incluir?: string;
  excluir?: string;
  limiteConexiones?: number;
}

export const LIMITE_NODOS = 5000;
export const LIMITE_CONEXIONES = 10000;

export async function analizar(raiz: string, opciones: OpcionesAnalisis): Promise<FlowDefinition> {
  const absoluta = resolve(raiz);

  // Un mismo modelo intermedio para los dos lenguajes: los extractores no
  // saben ni necesitan saber de dónde vino el código.
  const enGo = opciones.lenguaje === "go";
  const analisisGo = enGo ? analizarGo(absoluta) : undefined;
  const modelo = analisisGo ? analisisGo.modelo : analizarTypeScript(absoluta);

  let flujo: FlowDefinition;
  switch (opciones.modo) {
    case "modules":
      flujo = extraerModulos(modelo, basename(absoluta));
      break;
    case "functions": {
      const { funciones, llamadas } = analisisGo ?? analizarFunciones(absoluta);
      const pasa = (archivo: string) =>
        (!opciones.incluir || archivo.includes(opciones.incluir))
        && (!opciones.excluir || !archivo.includes(opciones.excluir));
      const filtradas = funciones.filter((f) => pasa(f.archivo));
      const nombres = new Set(filtradas.map((f) => f.nombre));
      flujo = extraerFunciones(
        filtradas,
        llamadas.filter((l) => nombres.has(l.desde) && nombres.has(l.hacia)),
        basename(absoluta),
      );
      break;
    }
    case "journey": {
      // Se toma el primer archivo con una función exportada: el punto de
      // entrada del recorrido. `--desde` lo acotará cuando exista.
      const archivos: string[] = [];
      const recoger = (dir: string): void => {
        for (const entrada of readdirSync(dir)) {
          if (["node_modules", ".git", "dist"].includes(entrada)) continue;
          const completa = join(dir, entrada);
          if (statSync(completa).isDirectory()) recoger(completa);
          else if (/\.[jt]sx?$/.test(entrada) && !entrada.includes(".test.")) archivos.push(completa);
        }
      };
      recoger(absoluta);
      let recorrido;
      for (const archivo of archivos.sort()) {
        recorrido = extraerRecorrido(archivo, archivo);
        if (recorrido) break;
      }
      if (!recorrido) throw new Error("No se encontró ninguna función exportada que sirva de punto de entrada.");
      flujo = extraerRecorridoDeNegocio(recorrido, basename(absoluta));
      break;
    }
    default:
      throw new Error(`El modo "${opciones.modo}" todavía no está implementado.`);
  }

  // Fallar aquí con un mensaje útil es mejor que emitir un archivo que la API
  // va a rechazar más tarde con un error genérico.
  const topeNodos = opciones.limiteNodos ?? LIMITE_NODOS;
  const topeConexiones = opciones.limiteConexiones ?? LIMITE_CONEXIONES;
  if (flujo.nodes.length > topeNodos) {
    throw new Error(
      `El análisis produjo ${flujo.nodes.length} nodos y supera el límite de ${topeNodos}. ` +
      "Acota el alcance con --incluir, --excluir o --desde.",
    );
  }
  if (flujo.edges.length > topeConexiones) {
    throw new Error(
      `El análisis produjo ${flujo.edges.length} conexiones y supera el límite de ${topeConexiones}. ` +
      "Acota el alcance con --incluir, --excluir o --desde.",
    );
  }
  return flujo;
}

export { analizarTypeScript } from "./adapters/typescript";
export { analizarGo, hayGoDisponible } from "./adapters/go";
export { extraerModulos } from "./extractors/modules";
export type { ModeloCodigo, Modulo, Referencia } from "./model";
