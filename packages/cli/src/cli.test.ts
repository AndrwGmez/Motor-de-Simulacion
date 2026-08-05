import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { DEMO_FLOW } from "@flowverse/core";
import { runCLI } from "./cli";

const temporaryDirectories: string[] = [];

afterEach(async () => {
  const { rm } = await import("node:fs/promises");
  await Promise.all(temporaryDirectories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })));
});

async function fixture(): Promise<{ directory: string; baseline: string; candidate: string }> {
  const directory = await mkdtemp(join(tmpdir(), "flowverse-cli-"));
  temporaryDirectories.push(directory);
  const baseline = structuredClone(DEMO_FLOW);
  const candidate = structuredClone(DEMO_FLOW);
  candidate.nodes[0]!.durationMs += 25;
  await Promise.all([
    writeFile(join(directory, "baseline.json"), JSON.stringify(baseline)),
    writeFile(join(directory, "candidate.json"), JSON.stringify(candidate)),
  ]);
  return { directory, baseline: "baseline.json", candidate: "candidate.json" };
}

function capture(): { writer: { write(value: string): void }; read(): string } {
  const values: string[] = [];
  return {
    writer: { write: (value) => { values.push(value); } },
    read: () => values.join(""),
  };
}

describe("flowverse CLI", () => {
  it("emits JSON validation output", async () => {
    const files = await fixture();
    const stdout = capture();
    const stderr = capture();
    const exitCode = await runCLI(["validate", files.baseline, "--json"], {
      cwd: files.directory,
      stdout: stdout.writer,
      stderr: stderr.writer,
    });

    expect(exitCode).toBe(0);
    expect(JSON.parse(stdout.read())).toMatchObject({ command: "validate", valid: true });
    expect(stderr.read()).toBe("");
  });

  it("writes SARIF and returns a failing PR policy exit code", async () => {
    const files = await fixture();
    const stdout = capture();
    const stderr = capture();
    const exitCode = await runCLI([
      "check",
      files.candidate,
      "--baseline",
      files.baseline,
      "--fail-on",
      "behavioral",
      "--format",
      "sarif",
      "--output",
      "artifacts/flowverse.sarif",
    ], {
      cwd: files.directory,
      stdout: stdout.writer,
      stderr: stderr.writer,
    });

    expect(exitCode).toBe(1);
    expect(stdout.read()).toBe("");
    expect(stderr.read()).toContain("artifacts/flowverse.sarif");
    const sarif = JSON.parse(await readFile(join(files.directory, "artifacts/flowverse.sarif"), "utf8"));
    expect(sarif).toMatchObject({ version: "2.1.0" });
    expect(sarif.runs[0].results).toEqual(expect.arrayContaining([
      expect.objectContaining({ level: "error", ruleId: "flowverse.semantic.behavioral" }),
    ]));
  });

  it("accepts simulation input from @file and repeated overrides", async () => {
    const files = await fixture();
    await writeFile(join(files.directory, "input.json"), JSON.stringify({
      payment: { status: "approved" }, inventory: { available: true },
    }));
    const stdout = capture();
    const exitCode = await runCLI([
      "simulate",
      files.baseline,
      "--input",
      "@input.json",
      "--fail-node",
      "ship",
      "--json",
    ], { cwd: files.directory, stdout: stdout.writer });

    expect(exitCode).toBe(0);
    expect(JSON.parse(stdout.read())).toMatchObject({
      command: "simulate",
      plan: { summary: { status: "failed" } },
    });
  });

  it("returns usage code 2 for invalid policies", async () => {
    const files = await fixture();
    const stderr = capture();
    const exitCode = await runCLI(["check", files.candidate, "--fail-on", "behavioral"], {
      cwd: files.directory,
      stderr: stderr.writer,
    });
    expect(exitCode).toBe(2);
    expect(stderr.read()).toContain("--fail-on requiere --baseline");
  });

  it("turns structural contract failures into SARIF validation findings", async () => {
    const files = await fixture();
    const invalid = structuredClone(DEMO_FLOW) as unknown as { nodes: Array<{ type: string }> };
    invalid.nodes[0]!.type = "unknown";
    await writeFile(join(files.directory, "invalid.json"), JSON.stringify(invalid));
    const stdout = capture();

    const exitCode = await runCLI(["check", "invalid.json", "--sarif"], {
      cwd: files.directory,
      stdout: stdout.writer,
    });

    expect(exitCode).toBe(1);
    const sarif = JSON.parse(stdout.read());
    expect(sarif.runs[0].results).toEqual(expect.arrayContaining([
      expect.objectContaining({ ruleId: "flow.schema.enum", level: "error" }),
    ]));
  });
});
