"use client";

import Link from "next/link";
import { useState } from "react";
import { authenticate, hasConfiguredApi } from "@/lib/account-service";

export default function AccessPage() {
  const [register, setRegister] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  return (
    <main className="auth-page">
      <Link className="brand auth-brand" href="/"><span className="brand-mark">FV</span><span>FlowVerse <b>3D</b></span></Link>
      <section className="auth-visual" aria-hidden="true">
        <div className="auth-universe">
          <span className="auth-orbit one" /><span className="auth-orbit two" /><span className="auth-orbit three" />
          <i className="auth-planet p1" /><i className="auth-planet p2" /><i className="auth-planet p3" /><i className="auth-planet p4" />
        </div>
        <div><span className="eyebrow">PROCESOS QUE PUEDES VER</span><h1>Entra en el flujo.<br />Entiende el sistema.</h1><p>Diseña, simula y analiza universos operativos en tres dimensiones.</p></div>
      </section>
      <section className="auth-panel">
        <div className="auth-card">
          <span className="eyebrow">{register ? "CREAR ESPACIO" : "BIENVENIDO DE NUEVO"}</span>
          <h2>{register ? "Crea tu cuenta" : "Accede a FlowVerse"}</h2>
          <p>{register ? "Tu primer universo está a unos pasos." : "Continúa construyendo universos."}</p>
          <form
            onSubmit={async (event) => {
              event.preventDefault();
              setError("");
              setLoading(true);
              const values = new FormData(event.currentTarget);
              try {
                await authenticate(register ? "register" : "login", {
                  email: String(values.get("email") ?? ""),
                  password: String(values.get("password") ?? ""),
                  displayName: register ? String(values.get("name") ?? "") : undefined,
                });
                window.location.assign("/");
              } catch (cause) {
                setError(cause instanceof Error ? cause.message : "No se pudo iniciar la sesión.");
                setLoading(false);
              }
            }}
          >
            {register && <div className="field"><label htmlFor="name">Nombre</label><input id="name" name="name" autoComplete="name" required /></div>}
            <div className="field"><label htmlFor="email">Correo</label><input id="email" name="email" type="email" autoComplete="email" defaultValue={hasConfiguredApi ? undefined : "demo@flowverse.dev"} required /></div>
            <div className="field"><label htmlFor="password">Contraseña</label><input id="password" name="password" type="password" autoComplete={register ? "new-password" : "current-password"} defaultValue={hasConfiguredApi ? undefined : "flowverse-demo"} minLength={register ? 12 : 1} required /></div>
            {error && <p className="dialog-error" role="alert">⚠ {error}</p>}
            <button type="submit" className="primary-button full" disabled={loading}>{loading ? "Abriendo universo…" : register ? "Crear cuenta" : "Entrar"}</button>
          </form>
          <div className="auth-switch">{register ? "¿Ya tienes cuenta?" : "¿Aún no tienes cuenta?"}<button type="button" onClick={() => setRegister((value) => !value)}>{register ? "Inicia sesión" : "Regístrate"}</button></div>
          {!hasConfiguredApi && <Link className="demo-access" href="/">Entrar directamente al modo demo →</Link>}
        </div>
      </section>
    </main>
  );
}
