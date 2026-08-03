import { defineConfig } from "vitest/config";

// Cada paquete corre sus propias pruebas: al extraerlos se quedaron colgando
// del corredor de la aplicación, lo que ataba la librería a su consumidor.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
    // `core` solo expone tipos y datos: no tener pruebas propias es correcto.
    passWithNoTests: true,
  },
});
