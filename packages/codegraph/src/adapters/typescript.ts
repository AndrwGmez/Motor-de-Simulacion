import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, resolve, dirname, sep } from "node:path";
import ts from "typescript";
import type { ModeloCodigo, Modulo, Papel, Referencia } from "../model";

/**
 * Adaptador de TypeScript y JavaScript.
 *
 * Usa el propio compilador de TypeScript, que ya es dependencia del proyecto:
 * no añade nada nuevo y resuelve la sintaxis real en vez de adivinar con
 * expresiones regulares.
 */

const EXTENSIONES = [".ts", ".tsx", ".js", ".jsx", ".mts", ".cts"];
const IGNORADOS = new Set(["node_modules", ".git", "dist", "build", ".next", "coverage"]);

// Paquetes que delatan el papel de un módulo. La lista es deliberadamente
// corta y explícita: es mejor clasificar de menos que inventar categorías.
const PERSISTENCIA = ["pg", "mysql", "mysql2", "mongodb", "mongoose", "prisma", "@prisma/client", "typeorm", "sequelize", "knex", "sqlite3", "redis", "ioredis"];
const SERVICIOS = ["axios", "node-fetch", "got", "undici", "superagent", "stripe", "@aws-sdk", "@azure/", "@google-cloud/", "nodemailer", "amqplib", "kafkajs"];

function esRelativo(especificador: string): boolean {
  return especificador.startsWith(".");
}

function coincide(especificador: string, lista: string[]): boolean {
  return lista.some((paquete) => especificador === paquete || especificador.startsWith(`${paquete}/`) || especificador.startsWith(paquete));
}

function archivos(raiz: string, actual = raiz, acumulado: string[] = []): string[] {
  for (const entrada of readdirSync(actual)) {
    if (IGNORADOS.has(entrada)) continue;
    const completa = join(actual, entrada);
    if (statSync(completa).isDirectory()) archivos(raiz, completa, acumulado);
    else if (EXTENSIONES.some((extension) => entrada.endsWith(extension)) && !entrada.includes(".test.")) {
      acumulado.push(completa);
    }
  }
  return acumulado;
}

function importaciones(codigo: string, ruta: string): string[] {
  const fuente = ts.createSourceFile(ruta, codigo, ts.ScriptTarget.Latest, true);
  const encontradas: string[] = [];
  const visitar = (nodo: ts.Node): void => {
    if ((ts.isImportDeclaration(nodo) || ts.isExportDeclaration(nodo)) && nodo.moduleSpecifier && ts.isStringLiteral(nodo.moduleSpecifier)) {
      encontradas.push(nodo.moduleSpecifier.text);
    }
    if (ts.isCallExpression(nodo) && nodo.expression.kind === ts.SyntaxKind.ImportKeyword) {
      const [primero] = nodo.arguments;
      if (primero && ts.isStringLiteral(primero)) encontradas.push(primero.text);
    }
    ts.forEachChild(nodo, visitar);
  };
  visitar(fuente);
  return encontradas;
}

/** Resuelve una importación relativa a la ruta del módulo al que apunta. */
function resolverInterna(desdeRuta: string, especificador: string, indice: Set<string>): string | undefined {
  const base = join(dirname(desdeRuta), especificador).split(sep).join("/");
  for (const candidato of [base, ...EXTENSIONES.map((e) => `${base}${e}`), ...EXTENSIONES.map((e) => `${base}/index${e}`)]) {
    if (indice.has(candidato)) return candidato;
  }
  return undefined;
}

export function analizarTypeScript(raizAbsoluta: string): ModeloCodigo {
  const raiz = resolve(raizAbsoluta);
  const rutas = archivos(raiz).map((completa) => relative(raiz, completa).split(sep).join("/")).sort();
  const indice = new Set(rutas);

  const modulos: Modulo[] = [];
  const referencias: Referencia[] = [];

  for (const ruta of rutas) {
    const codigo = readFileSync(join(raiz, ruta), "utf8");
    const especificadores = importaciones(codigo, ruta);

    let papel: Papel = "process";
    let motivo: string | undefined;
    for (const especificador of especificadores) {
      if (esRelativo(especificador)) continue;
      if (coincide(especificador, PERSISTENCIA)) { papel = "data"; motivo = especificador; break; }
      if (coincide(especificador, SERVICIOS)) { papel = "integration"; motivo = especificador; break; }
    }

    const partes = ruta.split("/");
    modulos.push({
      ruta,
      nombre: partes[partes.length - 1],
      directorio: partes.length > 1 ? partes[partes.length - 2] : "",
      papel,
      motivo,
    });

    for (const especificador of especificadores) {
      if (!esRelativo(especificador)) continue;
      const destino = resolverInterna(ruta, especificador, indice);
      if (destino && destino !== ruta) referencias.push({ desde: ruta, hacia: destino, clase: "import" });
    }
  }

  return { raiz, modulos, referencias };
}
