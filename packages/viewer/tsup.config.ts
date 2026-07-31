import { defineConfig } from "tsup";

export default defineConfig({
  entry: ["src/index.ts"],
  format: ["esm"],
  dts: true,
  sourcemap: true,
  clean: true,
  treeshake: true,
  // Las pruebas viven junto al código; no forman parte del artefacto.
  external: ["react", "react-dom", "three", "react-force-graph-3d", "@flowverse/core"],
});
