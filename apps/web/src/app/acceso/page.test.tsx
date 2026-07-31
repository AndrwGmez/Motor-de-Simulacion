import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

afterEach(() => {
  cleanup();
  vi.unstubAllEnvs();
  vi.resetModules();
});

describe("AccessPage", () => {
  it("no precarga credenciales demo cuando hay una API configurada", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "http://api.flowverse.test");
    const { default: AccessPage } = await import("./page");
    render(<AccessPage />);
    expect(screen.getByLabelText("Correo")).toHaveValue("");
    expect(screen.getByLabelText("Contraseña")).toHaveValue("");
  });

  it("mantiene el acceso rápido exclusivamente en modo demo", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_URL", "");
    const { default: AccessPage } = await import("./page");
    render(<AccessPage />);
    expect(screen.getByLabelText("Correo")).toHaveValue("demo@flowverse.dev");
    expect(screen.getByRole("link", { name: /modo demo/ })).toBeVisible();
  });
});
