import { FlatCompat } from "@eslint/eslintrc";

const compat = new FlatCompat({ baseDirectory: import.meta.dirname });

const config = [
  ...compat.extends("next/core-web-vitals", "next/typescript"),
  {
    ignores: ["next-env.d.ts", ".next/**", "coverage/**", "playwright-report/**", "test-results/**"],
  },
];

export default config;
