SHELL := /bin/bash

.DEFAULT_GOAL := help

.PHONY: help install dev dev-api dev-web up down logs contracts lint typecheck test test-all test-api test-api-race test-web test-e2e test-e2e-full build compose-build ci clean-generated

help:
	@awk 'BEGIN {FS = ":.*## "; printf "\nFlowVerse 3D\n\n"} /^[a-zA-Z_-]+:.*## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

install: ## Instala dependencias del workspace.
	corepack enable
	pnpm install --frozen-lockfile

dev: up ## Inicia el stack local completo.

dev-api: ## Inicia la API con el store configurado en el entorno.
	cd apps/api && go run ./cmd/api

dev-web: ## Inicia únicamente Next.js.
	pnpm dev

up: ## Construye e inicia PostgreSQL, API y web.
	docker compose up --build

down: ## Detiene los servicios locales.
	docker compose down

logs: ## Sigue los logs de los servicios locales.
	docker compose logs --follow

contracts: ## Valida JSON Schema, OpenAPI, AsyncAPI y fixtures.
	pnpm contracts:check

lint: ## Ejecuta los linters disponibles.
	pnpm lint
	cd apps/api && go vet ./...

typecheck: ## Comprueba TypeScript en los paquetes que lo soportan.
	pnpm typecheck

test: contracts test-api test-web ## Ejecuta pruebas unitarias y de contrato.

test-all: test test-api-race test-e2e ## Ejecuta unitarias, race detector y E2E.

test-api: ## Ejecuta pruebas Go.
	cd apps/api && go test ./...

test-api-race: ## Ejecuta pruebas Go con detector de carreras.
	cd apps/api && go test -race ./...

test-web: ## Ejecuta las pruebas del frontend.
	pnpm --filter @flowverse/web test

test-e2e: ## Ejecuta la suite Playwright (requiere Chromium instalado).
	pnpm test:e2e

test-e2e-full: ## Levanta el stack con Compose y ejecuta el recorrido real.
	docker compose up --build --wait --detach
	PLAYWRIGHT_BASE_URL=http://localhost:$${WEB_PORT:-3000} \
	FLOWVERSE_API_URL=http://localhost:$${API_PORT:-8080} \
	pnpm test:e2e:full; \
	status=$$?; \
	docker compose down; \
	exit $$status

build: ## Compila todos los componentes.
	pnpm build
	mkdir -p bin
	cd apps/api && go build -buildvcs=false -trimpath -o ../../bin/flowverse-api ./cmd/api

compose-build: ## Verifica los Dockerfiles construyendo las imágenes.
	docker compose build

ci: contracts lint typecheck test-api test-api-race test-web test-e2e build ## Reproduce los gates no interactivos de CI.

clean-generated: ## Elimina únicamente artefactos regenerables del fixture de rendimiento.
	find packages/contracts/fixtures/generated -mindepth 1 -maxdepth 1 -type f -delete 2>/dev/null || true
