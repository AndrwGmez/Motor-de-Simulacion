import { expect, test } from "@playwright/test";

test("crea un nodo, deshace y valida el flujo demo", async ({ page }) => {
  await page.goto("/proyectos/demo/flujos/pedidos");
  await expect(page.getByLabel("Nombre del flujo")).toHaveValue("Procesamiento de pedidos");
  await expect(page.getByTestId("flow-scene")).toBeVisible();

  await page.getByTestId("add-process").click();
  await expect(page.getByLabel("Inspector de propiedades").getByLabel("Nombre")).toHaveValue(/Proceso/);
  await page.getByTitle("Deshacer").click();

  await page.getByRole("button", { name: /Validar/ }).click();
  await expect(page.getByRole("dialog", { name: "Salud del flujo" })).toContainText("El flujo está listo para simular");
});

test("ejecuta, pausa y avanza el flujo", async ({ page }) => {
  await page.goto("/proyectos/demo/flujos/pedidos");
  await page.getByRole("button", { name: /Run Flow/ }).click();
  const dialog = page.getByRole("dialog", { name: "Configurar ejecución" });
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: /Ejecutar flujo/ }).click();
  await expect(page.getByRole("button", { name: "Pausar" })).toBeVisible();
  await page.getByRole("button", { name: "Pausar" }).click();
  await expect(page.getByRole("button", { name: "Avanzar un evento" })).toBeEnabled();
  await page.getByRole("button", { name: "Avanzar un evento" }).click();
});

test("ofrece importación y vista pública de solo lectura", async ({ page }) => {
  await page.goto("/proyectos/demo/flujos/pedidos");
  await page.getByRole("button", { name: "Importar JSON" }).click();
  await expect(page.getByRole("dialog", { name: "Importar un universo" })).toBeVisible();
  await page.goto("/compartir/demo-pedidos");
  await expect(page.getByText("Solo lectura", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Paleta de nodos")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Compartir/ })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Publicar/ })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Run Flow/ })).toHaveCount(0);
  await page.getByRole("button", { name: "Ver historial compartido" }).click();
  await expect(page.getByRole("dialog", { name: "Historial de ejecuciones" })).not.toContainText("run-demo");
});

test("publica explícitamente el borrador antes de compartir", async ({ page }) => {
  await page.goto("/proyectos/demo/flujos/pedidos");
  await page.getByRole("button", { name: /Publicar/ }).click();
  const dialog = page.getByRole("dialog", { name: "Publicar borrador" });
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: "Publicar versión" }).click();
  await expect(dialog).toContainText("Versión 1 publicada");
  await dialog.getByRole("button", { name: "Listo" }).click();
  await expect(page.getByRole("button", { name: /Publicar/ })).toContainText("V1");
});

test("crea un flujo mínimo y lo abre desde el proyecto", async ({ page }) => {
  await page.goto("/proyectos/demo");
  await page.getByRole("button", { name: /Nuevo flujo/ }).click();
  await page.getByLabel("Nombre").fill("Flujo mínimo");
  await page.getByRole("button", { name: "Crear y abrir borrador" }).click();
  await expect(page).toHaveURL(/\/proyectos\/demo\/flujos\/demo-flow-/);
  await expect(page.getByLabel("Nombre del flujo")).toHaveValue("Flujo mínimo");
  await expect(page.getByText("Operaciones comerciales", { exact: true })).toBeVisible();
});

test("conserva el proyecto y aplica las capacidades del rol editor", async ({ page }) => {
  await page.goto("/proyectos/customer-journey/flujos/pedidos");
  const breadcrumb = page.getByRole("link", { name: "Volver a proyectos" });
  await expect(breadcrumb).toContainText("Customer journey");
  await expect(page.getByRole("button", { name: /Publicar/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /Compartir/ })).toHaveCount(0);
  await breadcrumb.click();
  await expect(page).toHaveURL(/\/proyectos\/customer-journey$/);
});
