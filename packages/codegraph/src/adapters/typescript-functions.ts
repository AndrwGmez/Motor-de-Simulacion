import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";
import ts from "typescript";

/**
 * Funciones declaradas y llamadas entre ellas.
 *
 * El papel de cada función lo delata a quién llama: un cliente HTTP la convierte
 * en integración y un acceso a persistencia en nodo de datos. Es la misma
 * heurística del recorrido de negocio, aplicada aquí a granularidad de función.
 */

export interface Funcion {
  nombre: string;
  archivo: string;
  papel: "process" | "data" | "integration";
}

export interface Llamada {
  desde: string;
  hacia: string;
}

const PERSISTENCIA = /\b(pool|prisma|knex|sequelize|repositor|query|insert|update|delete)\b/i;
const SERVICIOS = /\b(axios|fetch|got|http|superagent|nodemailer)\b/i;
const IGNORADOS = new Set(["node_modules", ".git", "dist", "build", ".next"]);

function archivos(raiz: string, actual = raiz, acumulado: string[] = []): string[] {
  for (const entrada of readdirSync(actual)) {
    if (IGNORADOS.has(entrada)) continue;
    const completa = join(actual, entrada);
    if (statSync(completa).isDirectory()) archivos(raiz, completa, acumulado);
    else if (/\.[jt]sx?$/.test(entrada) && !entrada.includes(".test.")) acumulado.push(completa);
  }
  return acumulado;
}

export function analizarFunciones(raiz: string): { funciones: Funcion[]; llamadas: Llamada[] } {
  const funciones: Funcion[] = [];
  const llamadas: Llamada[] = [];

  for (const completa of archivos(raiz).sort()) {
    const ruta = relative(raiz, completa).split(sep).join("/");
    const fuente = ts.createSourceFile(ruta, readFileSync(completa, "utf8"), ts.ScriptTarget.Latest, true);

    const visitar = (nodo: ts.Node, dentroDe?: string): void => {
      let actual = dentroDe;
      if ((ts.isFunctionDeclaration(nodo) || ts.isMethodDeclaration(nodo)) && nodo.name) {
        const nombre = nodo.name.getText(fuente);
        const cuerpo = nodo.getText(fuente);
        const papel = PERSISTENCIA.test(cuerpo) ? "data" : SERVICIOS.test(cuerpo) ? "integration" : "process";
        funciones.push({ nombre, archivo: ruta, papel });
        actual = nombre;
      }
      if (ts.isCallExpression(nodo) && actual && ts.isIdentifier(nodo.expression)) {
        llamadas.push({ desde: actual, hacia: nodo.expression.getText(fuente) });
      }
      ts.forEachChild(nodo, (hijo) => visitar(hijo, actual));
    };
    visitar(fuente);
  }

  // Solo se conservan las llamadas entre funciones del propio proyecto.
  const propias = new Set(funciones.map((f) => f.nombre));
  return { funciones, llamadas: llamadas.filter((l) => propias.has(l.hacia) && l.desde !== l.hacia) };
}
