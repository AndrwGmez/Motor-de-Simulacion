import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
      // Las pruebas se ejecutan contra la fuente de los paquetes, no contra su
      // artefacto: así el ciclo de desarrollo no exige compilar antes de probar,
      // y un fallo señala la línea real y no el bundle.
      "@flowverse/core": fileURLToPath(new URL("../../packages/core/src/index.ts", import.meta.url)),
      "@flowverse/engine": fileURLToPath(new URL("../../packages/engine/src/index.ts", import.meta.url)),
      "@flowverse/viewer": fileURLToPath(new URL("../../packages/viewer/src/index.ts", import.meta.url)),
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    coverage: {
      reporter: ["text", "json-summary"],
      include: ["src/lib/**/*.ts", "src/store/**/*.ts"],
      // Umbral de cobertura: CI generaba el informe y no exigía nada, así que
      // no protegía de nada. Se fija en el nivel actual para que solo pueda
      // subir.
      thresholds: { lines: 65, functions: 65, statements: 65 },
    },
  },
});
