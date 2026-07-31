import { chromium } from "@playwright/test";
const BASE = "http://localhost:3000";
const N = Number(process.env.N ?? 5);
const browser = await chromium.launch();
const muestras = [];
for (let i = 0; i < N; i += 1) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
  const t = Date.now();
  await page.goto(`${BASE}/proyectos/demo/flujos/pedidos`);
  await page.getByLabel("Nombre del flujo").waitFor({ timeout: 60000 });
  const cascara = Date.now() - t;
  await page.getByTestId("flow-scene").waitFor({ timeout: 60000 });
  muestras.push({ cascara, escena: Date.now() - t });
  await page.close();
}
const med = (k) => muestras.map((m) => m[k]).sort((a, b) => a - b)[Math.floor(N / 2)];
console.log(`  cáscara del editor: ${muestras.map((m) => m.cascara).join(", ")} → mediana ${med("cascara")} ms`);
console.log(`  escena 3D montada:  ${muestras.map((m) => m.escena).join(", ")} → mediana ${med("escena")} ms`);
await browser.close();
