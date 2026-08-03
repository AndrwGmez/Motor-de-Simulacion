import { expect, test, type Page } from "@playwright/test";

/**
 * Recorrido contra la pila real: navegador → Next.js → API en Go → PostgreSQL,
 * incluido el WebSocket de ejecución. A diferencia de `e2e/`, aquí no existe el
 * modo demo: cada aserción atraviesa el contrato compartido, que es donde se
 * esconden las diferencias entre el serializador de Go y el validador del
 * navegador.
 */

const API = (process.env.FLOWVERSE_API_URL ?? "http://localhost:8080").replace(/\/$/, "");
const PASSWORD = "FlowverseE2EPassword123!";

test.describe.configure({ mode: "serial" });

let email = "";
let projectId = "";
let flowId = "";

test.beforeAll(() => {
  const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`;
  email = `e2e-${suffix}@flowverse.test`;
});

// Cada test recibe un contexto limpio, así que la sesión se rehace por prueba:
// mantiene los casos independientes y ejercita el login real en cada uno.
async function signIn(page: Page) {
  await page.goto("/acceso");
  await page.locator("#email").fill(email);
  await page.locator("#password").fill(PASSWORD);
  await page.getByRole("button", { name: "Entrar" }).click();
  await page.waitForURL("/", { timeout: 30_000 });
}

async function openEditor(page: Page) {
  await signIn(page);
  await page.goto(`/proyectos/${projectId}/flujos/${flowId}`);
  await expect(page.locator("canvas")).toHaveCount(1, { timeout: 30_000 });
  await expect(page.getByText("No pudimos abrir este universo")).toHaveCount(0);
}

async function idsFromUrl(page: Page) {
  const match = /\/proyectos\/([^/]+)(?:\/flujos\/([^/?#]+))?/.exec(page.url());
  return { projectId: match?.[1] ?? "", flowId: match?.[2] ?? "" };
}

test("registra una cuenta y persiste la sesión en la API", async ({ page }) => {
  await page.goto("/acceso");
  await page.getByRole("button", { name: "Regístrate" }).click();
  await page.locator("#name").fill("Recorrido E2E");
  await page.locator("#email").fill(email);
  await page.locator("#password").fill(PASSWORD);
  await page.getByRole("button", { name: "Crear cuenta" }).click();
  await page.waitForURL("/", { timeout: 30_000 });

  const me = await page.request.get(`${API}/v1/auth/me`);
  expect(me.ok()).toBe(true);
  expect((await me.json()).email).toBe(email);
});

test("crea un proyecto y un flujo y monta el editor tridimensional", async ({ page }) => {
  await signIn(page);

  await page.getByRole("button", { name: "Nuevo proyecto", exact: true }).click();
  await page.locator("[role=dialog] input, .modal-card input").first().fill("Operaciones");
  await page.locator("[role=dialog], .modal-card").getByRole("button", { name: /Crear/ }).click();

  await page.getByRole("link", { name: /Operaciones/ }).first().click();
  await page.waitForURL(/\/proyectos\//, { timeout: 30_000 });
  if (!/\/flujos\//.test(page.url())) {
    await page.getByRole("button", { name: /Nuevo flujo/i }).click();
    await page.locator("#flow-name").fill("Procesamiento de pedidos");
    await page.getByRole("button", { name: /Crear y abrir/i }).click();
    await page.waitForURL(/\/flujos\//, { timeout: 30_000 });
  }
  ({ projectId, flowId } = await idsFromUrl(page));
  expect(projectId).not.toBe("");
  expect(flowId).not.toBe("");

  // El borrador que crea la API debe abrirse sin violar el contrato: es la
  // regresión que dejaba el editor en "No pudimos abrir este universo".
  await expect(page.getByText("No pudimos abrir este universo")).toHaveCount(0);
  await expect(page.locator("canvas")).toHaveCount(1, { timeout: 30_000 });
});

test("guarda la edición en la API y la conserva al recargar", async ({ page }) => {
  await openEditor(page);

  const before = await (await page.request.get(`${API}/v1/flows/${flowId}/draft`)).json();
  await page.locator(".node-type-button").first().click();
  await expect(page.getByText("Guardado · API")).toBeVisible({ timeout: 30_000 });

  const after = await (await page.request.get(`${API}/v1/flows/${flowId}/draft`)).json();
  expect(after.nodes.length).toBe(before.nodes.length + 1);

  await page.reload();
  await expect(page.locator("canvas")).toHaveCount(1, { timeout: 30_000 });
  await expect(page.getByText("No pudimos abrir este universo")).toHaveCount(0);
});

test("ejecuta la simulación en el motor de Go y recibe los eventos por WebSocket", async ({ page }) => {
  await openEditor(page);

  const socket = page.waitForEvent("websocket", {
    predicate: (candidate) => candidate.url().includes("/live"),
    timeout: 30_000,
  });
  await page.getByRole("button", { name: /Run Flow|Nueva ejecución/ }).click();
  await page.locator("[role=dialog], .modal-card").getByRole("button", { name: /Ejecutar flujo/ }).click();

  const frames: string[] = [];
  (await socket).on("framereceived", (frame) => frames.push(String(frame.payload)));

  await expect.poll(async () => {
    const payload = await (await page.request.get(`${API}/v1/flows/${flowId}/runs`)).json();
    const runs: { status: string }[] = payload.items ?? payload.data ?? payload;
    return runs.find((run) => run.status === "completed") ? "completed" : runs[0]?.status ?? "none";
  }, { timeout: 60_000, intervals: [500] }).toBe("completed");

  // La API marca la ejecución como completada en cuanto el motor termina, pero
  // el WebSocket sigue reproduciendo eventos a velocidad de animación. Afirmar
  // aquí de golpe provocaba un fallo intermitente: había que esperar al
  // fotograma, no al estado.
  await expect.poll(() => frames.some((frame) => frame.includes("run.started")),
    { timeout: 30_000 }).toBe(true);
  await expect.poll(() => frames.some((frame) => frame.includes("run.completed")),
    { timeout: 30_000 }).toBe(true);
});

test("publica una versión inmutable y la comparte en solo lectura", async ({ page, baseURL }) => {
  await openEditor(page);

  await page.getByRole("button", { name: /Publicar/ }).click();
  const publishDialog = page.locator("[role=dialog], .modal-card");
  await publishDialog.getByRole("button", { name: "Publicar versión" }).click();
  await expect(page.getByText(/Versión \d+ publicada/)).toBeVisible({ timeout: 30_000 });
  await publishDialog.getByRole("button", { name: "Listo" }).click();

  await page.getByRole("button", { name: /Compartir/ }).click();
  await page.locator("[role=dialog], .modal-card").getByRole("button", { name: "Crear enlace público" }).click();

  const shareField = page.getByLabel("Enlace público");
  await expect(shareField).toHaveValue(/\/compartir\//, { timeout: 30_000 });
  const token = /compartir\/([^/?#\s"]+)/.exec(await shareField.inputValue())?.[1];
  expect(token, "el diálogo debe exponer el enlace público").toBeTruthy();

  // La vista pública no puede depender de la sesión del autor.
  const anonymous = await page.context().browser()!.newContext({ baseURL });
  const anonymousPage = await anonymous.newPage();
  await anonymousPage.goto(`/compartir/${token}`);
  await expect(anonymousPage.locator("canvas")).toHaveCount(1, { timeout: 30_000 });
  await expect(anonymousPage.getByText("No pudimos abrir este universo")).toHaveCount(0);
  await anonymous.close();
});
