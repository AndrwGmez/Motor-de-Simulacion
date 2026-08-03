import { readFileSync } from "node:fs";
import ts from "typescript";
import type { ClasePaso, Paso, Recorrido } from "../model";

/**
 * Extrae el recorrido de negocio de una función exportada.
 *
 * Recorre el cuerpo en orden y traduce cada construcción del lenguaje al tipo
 * de nodo que le corresponde. Es deliberadamente literal: sigue el flujo de
 * control tal como está escrito, sin intentar deducir intenciones.
 */

const PERSISTENCIA = /\b(pool|db|prisma|knex|repo|repositor)/i;
const SERVICIOS = /\b(axios|fetch|got|http|client|api)\b/i;

export function extraerRecorrido(rutaAbsoluta: string, nombreArchivo: string): Recorrido | undefined {
  const fuente = ts.createSourceFile(nombreArchivo, readFileSync(rutaAbsoluta, "utf8"), ts.ScriptTarget.Latest, true);

  let entrada: ts.FunctionDeclaration | undefined;
  ts.forEachChild(fuente, (nodo) => {
    if (!entrada && ts.isFunctionDeclaration(nodo) && nodo.name
      && nodo.modifiers?.some((m) => m.kind === ts.SyntaxKind.ExportKeyword)) {
      entrada = nodo;
    }
  });
  if (!entrada?.body || !entrada.name) return undefined;

  const pasos: Paso[] = [];
  let contador = 0;
  const nuevoId = (prefijo: string) => `${prefijo}_${(contador += 1)}`;

  const raiz = nuevoId("inicio");
  pasos.push({ id: raiz, clase: "entrada", etiqueta: entrada.name.text, siguientes: [] });

  const texto = (nodo: ts.Node): string => nodo.getText(fuente).replace(/\s+/g, " ").slice(0, 90);

  /** Devuelve el identificador del primer paso del bloque y encadena el resto. */
  const recorrer = (bloque: ts.Node, previo: string): string => {
    let anterior = previo;
    const conectar = (destino: string, etiqueta?: string) => {
      const paso = pasos.find((p) => p.id === anterior);
      if (paso) paso.siguientes.push({ destino, etiqueta });
    };

    const visitarSentencia = (sentencia: ts.Node): void => {
      if (ts.isIfStatement(sentencia)) {
        const decision = nuevoId("decision");
        pasos.push({ id: decision, clase: "decision", etiqueta: `¿${texto(sentencia.expression)}?`, siguientes: [] });
        conectar(decision);
        anterior = decision;

        const finRama = nuevoId("union");
        pasos.push({ id: finRama, clase: "proceso", etiqueta: "Continuar", siguientes: [] });

        const ramaSi = recorrer(sentencia.thenStatement, decision);
        pasos.find((p) => p.id === ramaSi)?.siguientes.push({ destino: finRama });
        if (sentencia.elseStatement) {
          const ramaNo = recorrer(sentencia.elseStatement, decision);
          pasos.find((p) => p.id === ramaNo)?.siguientes.push({ destino: finRama });
        } else {
          pasos.find((p) => p.id === decision)?.siguientes.push({ destino: finRama, etiqueta: "no" });
        }
        anterior = finRama;
        return;
      }

      if (ts.isForStatement(sentencia) || ts.isForOfStatement(sentencia) || ts.isWhileStatement(sentencia)) {
        const bucle = nuevoId("bucle");
        pasos.push({ id: bucle, clase: "bucle", etiqueta: `Para cada ${texto(sentencia).slice(0, 50)}`, siguientes: [] });
        conectar(bucle);
        anterior = bucle;
        return;
      }

      if (ts.isThrowStatement(sentencia)) {
        const fallo = nuevoId("fallo");
        pasos.push({ id: fallo, clase: "fin_error", etiqueta: `Error: ${texto(sentencia.expression).slice(0, 60)}`, siguientes: [] });
        conectar(fallo);
        return;
      }

      if (ts.isReturnStatement(sentencia)) {
        const fin = nuevoId("fin");
        pasos.push({ id: fin, clase: "fin_ok", etiqueta: "Resultado devuelto", siguientes: [] });
        conectar(fin);
        return;
      }

      // Una sentencia cualquiera: su clase depende de a quién llame.
      const contenido = texto(sentencia);
      let clase: ClasePaso = "proceso";
      if (PERSISTENCIA.test(contenido)) clase = "datos";
      else if (SERVICIOS.test(contenido)) clase = "integracion";
      const paso = nuevoId(clase);
      pasos.push({ id: paso, clase, etiqueta: contenido.slice(0, 70), siguientes: [] });
      conectar(paso);
      anterior = paso;
    };

    if (ts.isBlock(bloque)) bloque.statements.forEach(visitarSentencia);
    else visitarSentencia(bloque);
    return anterior;
  };

  const ultimo = recorrer(entrada.body, raiz);
  if (!pasos.find((p) => p.id === ultimo)?.siguientes.length) {
    const fin = nuevoId("fin");
    pasos.push({ id: fin, clase: "fin_ok", etiqueta: "Fin del recorrido", siguientes: [] });
    pasos.find((p) => p.id === ultimo)?.siguientes.push({ destino: fin });
  }
  return { entrada: raiz, pasos };
}
