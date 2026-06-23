# Pendientes / Roadmap — backend

## Plantillas buf INTERNAS fuera del deliverable (HECHO, 2026-06-23)

**Bug:** el entregable traía dos plantillas buf que son tooling INTERNO de la
plataforma, no parte del proyecto exportado del usuario:
- `protobuf/buf.docs.gen.yaml` → genera el openapi del **PANEL** escribiendo en
  `../milton-prism-panel` (vía el symlink). Solo-panel.
- `protobuf/buf.deliverable.openapi.yaml` → plantilla del **pipeline de la
  plataforma** que emite `docs/openapi.yaml` durante la generación. El agente la
  corre, pero la plantilla en sí es interna.

**Fix — `core/services/migration/application/assembler/assembler.go` (assembler-only):**
- **Go** (`isSkeletonFile`): el switch de buf pasó de `{buf.yaml, buf.go.gen.yaml,
  buf.docs.gen.yaml, buf.deliverable.openapi.yaml}` a SOLO `{buf.yaml,
  buf.go.gen.yaml}`. Quedan las dos configs user-facing: `buf.yaml` (módulo:
  lint/breaking/deps, para que el usuario regenere SUS stubs) y `buf.go.gen.yaml`
  (template de codegen Go).
- **Python / Node / Rust** (`isSkeletonFilePython|Node|Rust`): el switch pasó de
  `{buf.yaml, buf.docs.gen.yaml, buf.deliverable.openapi.yaml}` a SOLO `{buf.yaml}`
  — el módulo es lo único que el usuario necesita para regenerar con su propio
  template; `buf.go.gen.yaml` ya estaba excluido en esos perfiles (sigue así).
- **Defensa-en-profundidad:** nueva `isInternalBufTemplate(path)` ({buf.docs.gen.yaml,
  buf.deliverable.openapi.yaml}) en el loop de merge de artifacts — si el agente
  llegara a persistir las plantillas como artefactos, se descartan ahí también
  (no solo en el filtro de skeleton). El `docs/openapi.yaml` GENERADO **sí sigue
  enviándose** (no lo toca el filtro).
- Referencias al symlink del panel `milton-prism-panel` ya estaban excluidas vía
  `skipDir*` en TODOS los perfiles (sin cambio).

**Qué queda en el deliverable por perfil:** Go → `buf.yaml` + `buf.go.gen.yaml`;
Python/Node/Rust → solo `buf.yaml`. **0** `buf.docs.gen.yaml` / **0**
`buf.deliverable.openapi.yaml` en cualquier perfil.

**Tests (verdes) — `assembler_profile_test.go`:** fixture añade
`buf.deliverable.openapi.yaml`; aserciones negativas para ambas plantillas internas
en Go/Python/Node/Rust; `TestAssemble_DocsOpenAPISurvives` ahora también inyecta las
plantillas como artifacts y afirma que se descartan, mientras `docs/openapi.yaml`
sobrevive. `go build ./...` + `go test ./core/services/migration/...` verdes.

## Cancel/Delete de migración — modelo CORREGIDO (HECHO, 2026-06-23)

**Bug:** una migración `READY` se podía CANCELAR (no debía) y NO se podía ELIMINAR
(sí debía). Causa: cancel/delete decidían con `isTerminalState` (={PUSHED, FAILED,
CANCELLED, RESTRUCTURING_READY}); READY caía como "no terminal" → cancelable y
no-eliminable. El modelo estaba al revés para los estados "terminados pero no
terminal-enum" (READY).

**Single-source-of-truth (complementarios y totales sobre los estados reales):**
- **CANCELABLE = en proceso:** `PENDING, ANALYZING, DESIGNING, AWAITING_APPROVAL,
  GENERATING, TESTING` (`isCancelableMigrationState`).
- **ELIMINABLE = NO en proceso:** `READY, PUSHED, FAILED, CANCELLED,
  RESTRUCTURING_READY` (`isDeletableMigrationState`). **READY ahora es ELIMINABLE.**
- Regla del usuario: "no se elimina lo que está en curso, sí se cancela; lo
  terminado se elimina, no se cancela".

### Cambios (1 línea c/u) — `core/services/migration/application/service.go`
- Nuevas `isCancelableMigrationState(state)` (6 de proceso) y
  `isDeletableMigrationState(state)` (el resto, INCLUYE READY); complementarias.
- `CancelMigration`: rechaza si `!isCancelableMigrationState` → MIG202
  (`ErrInvalidStateTransition`). Antes cancelaba cualquier no-terminal (incl. READY).
- `DeleteMigration`: permite si `isDeletableMigrationState` (incl. READY); rechaza
  en-proceso con MIG202. Antes bloqueaba READY.
- `isTerminalState` se conserva intacto (otros usos: INV-2 RESTRUCTURING_READY, etc.).
- Sin proto / sin gateway (solo lógica de guard). Deploy: `infra/build.sh
  migration-services` → `strings infra/bin/migration_service | grep -c
  isDeletableMigrationState` = 1 (y isCancelable = 1) → `compose up -d --build
  --force-recreate migration-services` (entorno `local`, gateway :8083).

### Tests (verdes) — `application/cancel_delete_guard_test.go` + `service_test.go`
- `TestCancelDeleteStateGuards`: tabla READY cancelable=false/deletable=true,
  GENERATING cancelable=true/deletable=false; afirma complementariedad.
- `TestDeleteMigration_DeletableState_Success` (READY+terminales),
  `TestDeleteMigration_InProgressState_Rejected`,
  `TestCancelMigration_FromInProgress_Success`,
  `TestCancelMigration_NotInProgress_Rejected` (READY+terminales). INV-2 intacto.

### Evidencia E2E (gateway :8083, dev@prism.local user 10004 / system user 10013)
- **D1:** `:cancel` sobre READY (disposable mig43 forzada a READY en Mongo) →
  400 MIG202; sigue READY.
- **D2:** `DELETE` sobre READY mig43 → 200 `{}`; soft-deleted (fuera del List,
  GET 404 MIG201). No se tocó ninguna de las 8 celdas certificadas.
- **D3:** `:cancel` PENDING (mig42) → 200 CANCELLED; `:cancel` GENERATING (mig44) →
  200 CANCELLED; `DELETE` GENERATING (mig44) → 400 MIG202.
- **D4:** `go build ./...` + `go test ./core/services/migration/...` verdes.
- **Sin regresión:** las 8 celdas (19/21/23/24/28/34/38/41) siguen READY/200; no se
  borraron. Limpieza: disposables mig42/43/44 eliminadas.

## sortBy + filtros SERVER-SIDE en `ListMigrations` — HECHO (2026-06-23)

**Ordenamiento (order_by AIP-132) y filtros de matriz (topology/protocol/language)
resueltos 100% en el API/Mongo, NO en el frontend.** Una pasada de proto
(`buf lint` 0 + go.gen + docs.gen → `panel/openapi.yaml` por symlink), gateway
redeployado.

### Proto (1 línea c/u)
- `services/migration/v1/migration_service.proto`: `string order_by = 3` en
  `ListMigrationsRequest` (AIP-132, p.ej. `"create_time desc"`; default
  `"create_time desc"`; campos permitidos: create_time, topology, protocol, state,
  language; campo fuera de allowlist ⇒ INVALID_ARGUMENT MIG110).
- `types/migration/v1/migration.proto`: en `MigrationsFilter` se añadieron
  `optional TargetTopology topology = 6`, `optional Transport protocol = 7`,
  `optional TargetLanguage language = 8` (filtran contra los campos denormalizados
  que ya alimentan el índice de unicidad). Ya existían owner/repo/state/states/
  analysis_summary_id.

### Backend (archivos)
- `infrastructure/repositories/mongo_migration_repository.go`: `parseOrderBy`
  (allowlist field→bson, dir asc/desc, default `create_time desc`, tie-break por
  `create_time` cuando el campo primario no es create_time; field desconocido o
  dirección inválida ⇒ `domain.ErrInvalidOrderBy`); `List(...)` ahora aplica el
  `sort` parseado + los filtros topology/protocol/language; índices compuestos
  idempotentes en el constructor: `owner_create_time`, `owner_state_create_time`,
  `owner_topology_create_time`, `owner_protocol_create_time`,
  `owner_language_create_time`.
- `domain/errors.go`: `MIG110 ErrInvalidOrderBy` (Failure_Invalid_Order_By).
- `infrastructure/grpc_handlers/migration_handler.go`: `MIG110` → InvalidArgument
  en `mapError`; `ListMigrations` pasa `req.GetOrderBy()` al servicio.
- `application/service.go`, `ports/migration_repository.go`, `mocks/mocks.go`:
  firma `List(ctx, filter, orderBy, params)` propagada.
- Decisión documentada: campo de order_by fuera de la allowlist ⇒ **error
  InvalidArgument (MIG110)**, NO se ignora silenciosamente — el cliente nunca
  recibe un orden no verificado.

### Deploy + CERTIFICACIÓN (gateway :8083, dev@prism.local 10004; EdDSA bearer)
- `strings migration_service | grep -c orderBy/order_by` = 15; gateway = 5.
- **S1 sort (server-side):** `order_by=create_time desc` → [41,40,38,34,28,24,23,21,19];
  `asc` → exactamente inverso [19,21,23,24,28,34,38,40,41]. `order_by=state asc` →
  READY(7) agrupados primero, CANCELLED(10)=mig40 al final. `order_by=topology asc`
  → MICROSERVICES(1) [34,23,21,19] luego MONOLITH(2) [41,40,38,28,24]; `desc` invierte
  los grupos (tie-break create_time desc).
- **S2 filtros (server-side, total_size correcto):** topology=MONOLITH→5
  (41,40,38,28,24); protocol=HTTP→5 (41,40,38,34,28); language=RUST→1 (38);
  language=PYTHON→3; language=NODE→1 (34); MICROSERVICES→4 (34,23,21,19); combo
  HTTP+MONOLITH→4 (41,40,38,28); combo RUST+order_by=create_time desc→1 (38).
- **S3 order_by inválido:** `owner_user_id desc` → HTTP 400 `MIG110`
  InvalidArgument; `state sideways` → 400; sin order_by → 200 (default OK).
- **S4 índices:** `db.migrations.getIndexes()` muestra los 5 índices nuevos; counts
  con filtro = total_size devuelto.
- **S5 sin regresión:** `go build ./...` + `go test ./core/services/migration/...`
  verdes (incl. `order_by_test.go` parseOrderBy: allowlist + InvalidArgument +
  rechazo `$where`). openapi del panel regenerado conserva delete/cancel/approveDesign
  + AuthSchemeDetection/targetAuthScheme/costEstimated y gana
  orderBy/filter.topology/filter.protocol/filter.language.

### Nota (dato preexistente, NO bug)
Los filtros operan sobre los campos **denormalizados** top-level
(`topology`/`protocol`/`language`). Las migraciones más viejas (19/21/23/24)
se crearon antes de denormalizar protocol/language ⇒ esos campos están ausentes
(=0/UNSPECIFIED) aunque su `target_bytes` sí tenga el valor real. Por eso un
`filter.protocol=HTTP` excluye mig24 (GO+HTTP en bytes, pero protocol denorm
ausente). Pendiente opcional: backfill de protocol/language denormalizado para
los 4 registros viejos si se quiere que sus filtros por matriz coincidan con el
TargetConfig. No afecta records nuevos (denormalizan en Create).

### PASO SIGUIENTE — FRONTEND (del agente frontend, tras `npm run gen:api`)
- En `MigrationsPage.tsx`: controles de **sort** (ARQUITECTURA→`topology`,
  PROTOCOLO→`protocol`, ESTADO→`state`, CREADO→`create_time`, cada uno con
  toggle asc/desc) que arman el string `order_by` (p.ej. `"topology desc"`) y lo
  pasan al API; y controles de **filtro** (topology/protocol/language) que setean
  `filter.topology`/`filter.protocol`/`filter.language`. Al cambiar cualquiera,
  recargar la lista (server-side; NO ordenar/filtrar en el cliente). El api-client
  TS se regenera con `npm run gen:api` desde el `panel/openapi.yaml` ya actualizado
  (NO corrido aquí — es del frontend).

## Auth-JWT (detección en ANÁLISIS + implementación en GENERACIÓN) + cost_estimated — HECHO (2026-06-23)

**Detección de auth (análisis): HECHO.** **Propagación + generación JWT: HECHO.**
**`cost_estimated` en `UsageRecord`: HECHO.** Una sola pasada de proto
(`buf lint` + `buf generate` go.gen + docs.gen → `panel/openapi.yaml` por symlink),
gateway redeployado.

### A — Proto (una pasada)
- `types/analysis/v1/analysis.proto`: `enum AuthScheme{UNSPECIFIED,NONE,JWT,OAUTH2,
  SESSION_COOKIE,API_KEY,BASIC}` + `message AuthSchemeDetection{scheme, scheme_name,
  signature_alg, token_header, claims[], confidence, unknown, evidence[]}` +
  `AuthSchemeDetection auth_scheme_detection = 34` en `AnalysisSummary`.
- `types/migration/v1/migration.proto`: `AuthScheme target_auth_scheme = 6`
  (override, en `TargetConfig`; import de `analysis/v1`) + `string auth_scheme = 8`
  + `string auth_signature_alg = 9` en `ServiceGenerationSpec`.
- `types/billing/v1/billing.proto`: `bool cost_estimated = 11` en `UsageRecord`.
- Símbolos verificados (`strings`): analysis_worker `AuthSchemeDetection`,
  migration_service `auth_scheme`/`CostEstimated`, generation_worker
  `Auth / Validation`/`authSchemeSection`, gateway `AuthSchemeDetection`.

### B — Detección (análisis, determinístico PURO; sin LLM)
- `AuthSchemeDetector` (`core/worker/analysis/infrastructure/adapters/auth_scheme_detector.go`,
  molde de `database_detector.go`) + **stage 3e** en `pipeline.go` (no-fatal, espejo de 3c),
  wireado en `analysis-worker/main.go`. Persistido como `auth_scheme_detection_bytes`
  (worker `mongo_summary_writer.go`) y leído en el repo de análisis
  (`mongo_analysis_summary_repository.go`: campo bson + unset en re-análisis +
  `unmarshalAuthSchemeDetection`). Señales: paquetes (composer firebase/php-jwt·tymon/jwt-auth,
  pypi pyjwt·flask-jwt-extended·simplejwt·python-jose, npm jsonwebtoken·jose·passport-jwt·@nestjs/jwt,
  maven jjwt·java-jwt; oauth2 authlib·spring-oauth2·laravel/passport; basic passport-http·flask-httpauth)
  > config `.env` (JWT_SECRET⇒HS*, JWT_PUBLIC_KEY⇒RS*, JWT_ALGO explícito) >
  header `Authorization: Bearer` (reusa walk/exclusiones del SecurityScanner) >
  default de framework (Laravel/Symfony/Django/Rails/CodeIgniter ⇒ session_cookie).
  `none` honesto (`unknown=true`) cuando no hay señal. Aliases en `analysis/domain`.
  Tests: `auth_scheme_detector_test.go` (12 casos: JWT por paquete ×7, OAuth2, HS/RS/EdDSA
  por .env, header Bearer débil, none honesto, framework default, árbol vendored ignorado).

### C — Generación (implementar el auth detectado)
- `GetGenerationPackage` (`migration/application/service.go`): auth efectivo =
  `target_auth_scheme` (override) ?? `summary.auth_scheme_detection.scheme`;
  propaga `auth_scheme`+`auth_signature_alg` a cada `ServiceGenerationSpec`.
  Helper `authSchemeToken`. El worker (`mongo_generation_package_reader.go`) resuelve
  lo mismo de forma autónoma: override del `target` o lectura cross-DB del summary
  linkeado (`milton_prism_analysis.analysis_summaries`), degrada a "none" best-effort.
- `ports.InvokeRequest`/`ServiceSpec`/`GenerationPackage` ganan `AuthScheme`+`AuthSignatureAlg`;
  propagados en `pipeline.go` y `claude_agent_invoker.go`. Nuevo `authSchemeSection(profile,
  protocol,scheme,sigAlg)` en `workspace.go`/`writeCombinedPrompt` (molde de `transportSection`):
  **v1 GENERA solo JWT y none**; otros esquemas detectados ⇒ nota honesta (sin guess).
  JWT por-stack: golang-jwt/v5 (Go), PyJWT (Python), jsonwebtoken|jose (Node),
  jsonwebtoken-crate (Rust); común: leer token de Authorization: Bearer, secret/clave/iss/aud/claims
  de `.env` (NUNCA hardcodear), rechazar alg=none, error tipado→401/UNAUTHENTICATED,
  interceptor gRPC | middleware HTTP, `.env.example`, cubierto por el build gate + un test.
  Sección "Auth / Validation" añadida a los 4 profile docs (go/node/rust/python).
- Billing: `cost_estimated` seteado en el `UsageRecord` (true cuando el costo es estimado/
  subscription, false con costo real de API) en `finalizeGenerationBilling`
  (`UsageSpend.CostEstimated`), forwardeado en `billing_client_adapter.go` y persistido/leído
  en `mongo_usage_repository.go` (`cost_estimated` bson). Tests:
  `generation_billing_test.go` (estimado⇒true / real⇒false).

### CERTIFICACIÓN (gateway :8083, dev@prism.local 10004; apikey mode ⇒ costo real)
- **A1 detección e2e:** análisis de flask-realworld (repo 10001) →
  `auth_scheme_detection.scheme=AUTH_SCHEME_JWT` confidence 0.95, evidence
  `flask-jwt-extended`+`PyJWT`, tokenHeader `Authorization` (summary 10044, branch
  `dependabot/pip/werkzeug-0.15.3`; el branch `develop` NO existe en el remote, fue
  descartado). `none` honesto: análisis de notiplan (repo 10002, summary 10035) →
  `scheme=AUTH_SCHEME_NONE`, `unknown=true`, evidence vacío. Worker log:
  `auth scheme detection done summary_id=10044 scheme=JWT`.
- **A2 propagación:** migración 41 (Python+HTTP+monolito, reusa 10044) → worker log
  `generation package auth migration_id=41 scheme=jwt sig=` (resuelto del summary detectado).
- **A2-unit:** `TestGetGenerationPackage_AuthFromDetection` (jwt+HS256 del summary) +
  `TestGetGenerationPackage_AuthOverrideWins` (override corta el fetch del summary).
- **A5:** `go build ./...` + `go test ./core/worker/analysis/... ./core/services/migration/...
  ./core/worker/generation/... ./core/services/billing/... ./core/services/analysis/...`
  verdes. `buf lint` 0; openapi del panel regenerado conserva los ops delete/cancel
  y gana `AuthSchemeDetection`/`targetAuthScheme`/`costEstimated`.
- **A3 generación JWT real (CERTIFICADA):** mig41 (Python+HTTP+monolito, reusa
  summary 10044 con JWT detectado) → **READY**, service `app` status=done,
  **gates=True** (build gate del ZIP verde), 35 files, costo real $5.2468 (apikey).
  El deliverable (`:downloadDeliverable` → 200, 89 KB, 35 archivos) incluye el
  middleware/guard JWT generado:
    - `core/services/app/infrastructure/http/auth.py` — dependencia FastAPI con
      **PyJWT** (`jwt.decode`): lee key/iss/aud de `JwtSettings` (env/.env), **nunca
      hardcodeado**; rechaza `alg=none` y algoritmos fuera del configurado; lee
      `Authorization: Bearer`; error tipado `ERR_UNAUTHENTICATED` (APP401)→401 sin leak.
    - `core/shared/auth/extractor.py` — extractor JWT para gRPC (Bearer en metadata).
    - `core/services/app/.env.example` — `JWT_SECRET=<...>` (con guía `openssl rand`).
    - `core/services/app/tests/test_auth.py` — valid/missing/expired/wrong-signature/
      `alg=none` cubiertos.
    - **0 secretos hardcodeados** (grep del ZIP vacío).
- **A4 cost_estimated (CERTIFICADA):** el `usage_record` GENERATION de mig41 tiene
  `costEstimated=false` (apikey ⇒ costo real $5.2468, model `claude-opus-4-8[1m]`);
  log `GENERATION spend recorded migration_id=41 ... estimated=false`. El caso
  `estimated=true` (subscription) lo certifica el unit
  `TestFinalizeGenerationBilling_RecordsEstimatedSpend` (este entorno es apikey-only).
- **A5 sin regresión:** las 7 celdas vivas (19/21/23/24/28/34/38) siguen READY.
  mig41 queda como **8ª celda READY** y es la celda de certificación Auth-JWT
  (NO se borra: una migración READY no es eliminable por diseño — guard MIG202,
  preserva el deliverable). mig40 (intento sobre branch `develop` inexistente)
  quedó CANCELLED.

### PASO SIGUIENTE — FRONTEND (del agente frontend, tras `npm run gen:api`)
- Mostrar `auth_scheme_detection` (scheme + signature_alg + evidence) en el
  `ArchitectureDocPanel` (junto a `databaseDetection`/`architecturalPattern`).
- Selector de override `target_auth_scheme` en el wizard de creación de migración
  (UNSPECIFIED = usar el detectado; v1 genera JWT/none, otros como nota honesta).
- Label "estimado" en Usage & Billing cuando `usageRecord.cost_estimated == true`
  (no presentar un costo de subscription como dólares facturados).
- El api-client TS se regenera con `npm run gen:api` desde el `panel/openapi.yaml`
  ya actualizado (NO corrido aquí — es del frontend).

### Nota A3 (generación JWT real)
La corrida real de mig41 ejercita el `authSchemeSection` JWT inyectado en el prompt
(Python/HTTP ⇒ PyJWT + dependencia FastAPI). El deliverable debe incluir el
middleware/guard de validación JWT que lee de `.env` (0 secretos hardcodeados) y
compilar (gate del ZIP). cost_estimated del `usage_record` GENERATION: `false` en
apikey mode (costo real del provider). Evidencia adjunta al cerrar la corrida.



## Usage & Billing — RecordUsage DESBLOQUEADO + gasto de GENERACIÓN contabilizado (2026-06-23)

**Item C (RecordUsage rechazado con BIL101 Failure_System_User_Required) — DESBLOQUEADO.**
La causa raíz era que el handler de billing resuelve `isSystem` de la SESIÓN cacheada
(`session.SystemUser`), no del claim del token; mintar un token con `system_user:true`
no bastaba. Solución (Design A, sin proto):

1. **Token system** — `core/internal/svc/system_token.go`: `(*Services).SystemAccessToken(ctx)`
   (a) siembra/reusa una sesión system en el cache compartido (`SaveSession`,
   `SystemUser:true`, `sid` fresco, `ExpiresAt` corto = TTL 5 min, `IsRevoked:false`,
   `UserID = SystemUserID = 1` RESERVADO — por debajo del piso de la secuencia humana
   10001, nunca colisiona); (b) minta PASETO con esa `sid` y
   `user_properties{user_id:1, system_user:true}` usando la signKey del binario
   (encapsulada, solo migration-services minta); (c) cachea in-process y re-minta cuando
   quedan <60 s. Mitigaciones del blueprint respetadas: TTL corto, id reservado, scope,
   y el token NUNCA se loguea (verificado: 0 `v4.public.` / 0 signKey en logs — B4).
2. **Scope a SOLO RecordUsage** — `billing_client_adapter.go`: `RecordUsage` ya NO
   forwardea el token del usuario; construye un contexto outgoing FRESCO con
   `authorization:<token system>` (deriva del ctx para deadline, reemplaza metadata).
   `GetUserPlan` y el nuevo `CountUsageRecords` SIGUEN con el token del usuario. El
   `tokenProvider` (`SystemTokenProvider`) se inyecta por constructor; `wire.go` pasa
   `svc.SystemAccessToken`. Test de scope: `billing_token_scope_test.go` (system token sí,
   token de usuario NO se filtra; fallback sin provider forwardea inbound).
3. **Captura de modelo** — `output.go` parsea `modelUsage` (toma el model con más tokens
   vía `DominantModel()`); `Model` añadido a `ports.InvokeResult`, worker
   `ServiceGenerationRecord`, su doc Mongo (`mongo_generation_store.go`) y el reader de
   migration (`mongo_generation_result_reader.go`); poblado en `pipeline.go` `persist:`.
   Tests: `model_usage_test.go`.
4. **Tabla de precios** — `core/services/billing/domain/pricing.go`:
   `EstimateCostUSD(model, in, cacheCreate, cacheRead, out)` por MTok USD
   (opus-4-8/4-7/4-6 5/25 cache 6.25/0.50, sonnet-4-6 3/15 3.75/0.30, haiku-4-5 1/5
   1.25/0.10, fable-5 10/50 12.50/1.00). Fallback model desconocido → opus-4-8
   (conservador, nunca sub-cuenta). Tests: `pricing_test.go`.
5. **Gasto de GENERACIÓN (3a)** — registrado en **migration-services** (el worker no
   tiene signKey). Enganche = reconciliación IDEMPOTENTE en `GetMigration`
   (`service.go` `finalizeGenerationBilling`): cuando una migración se observa en estado
   terminal de generación (READY/FAILED), si NO existe ya un `usage_record` GENERATION
   para ella (`billing.CountUsageRecords`), suma tokens (`TokensIn = Σ(input+cache_creation
   +cache_read)`, `TokensOut = Σ(output)` vía nuevo `reader.ReadUsageTotals`), costo =
   `total_cost_usd` real si >0 (apikey) o estimado por la tabla (subscription, total_cost=0),
   atribuido a `Migration.OwnerUserId`, y llama `RecordUsage(operation=GENERATION)`.
   Idempotente (no doble-cuenta), best-effort (un fallo de billing nunca rompe el read).
   Tests: `generation_billing_test.go` (estimado/real/idempotente/sin-tokens/error-swallowed).
6. **PENDIENTE (requiere proto):** flag `cost_estimated` en `UsageRecord`. Hoy el estimado
   se distingue SOLO en el log server-side (`estimated=true/false`); no hay campo en el
   record. Diferido a la pasada de proto (NO tocado aquí para no regenerar `panel/openapi.yaml`).

### Certificación E2E (gateway :8083, dev@prism.local 10004; usage_records viven en `milton_prism_analysis`)
- **B1 + B3 (real, apikey):** `GET /v1/migrations/28` → 200; log
  `GENERATION spend recorded migration_id=28 owner=10004 tokensIn=4120213 tokensOut=51419
  costUSD=5.1753 estimated=false`. Persistió el PRIMER `usage_record` (antes 0 — BIL101
  bloqueaba TODO). Visible vía `GET /v1/usageRecords?migrationId=28` y
  `GET /v1/users/10004/usage` (línea GENERATION). Reconciliación al cierre de las 7 celdas
  vivas (19/21/23/24/28/34/38) → 7 records GENERATION, 0 duplicados (idempotencia).
- **B3 (estimado, subscription):** simulando `total_cost_usd=0` + `model=opus-4-8` en mig34 →
  `costUSD=13.2153 estimated=true` (2 451 510 in @5 + 38 310 out @25, exacto). Restaurado el
  costo real y borrado el record simulado tras certificar.
- **B2:** `GET /v1/users/10004/plan` → 200 (enterprise) con el token del usuario — quota
  lookups intactos (GetUserPlan NO usa el provider system).
- **B4:** 0 tokens PASETO / 0 signKey en logs de migration-services; sesión system TTL 5 min,
  id reservado 1, scope solo RecordUsage (test de scope verde).
- **B5:** `go build ./...` + `go test ./core/services/migration/... ./core/services/billing/...
  ./core/worker/generation/...` verdes; 7 celdas vivas siguen READY/200.
- Deploy: `infra/build.sh {migration-services,generation-worker,analysis-services}` →
  `compose up -d --build --force-recreate` (sin gateway, sin cambio de proto).
  `strings bin/migration_service | grep -c SystemAccessToken` = 4; `finalizeGenerationBilling`
  = 2; `EstimateCostUSD`/`GENERATION spend recorded` = 1; worker `DominantModel` = 3.

## Eje PROTOCOLO (gRPC | HTTP) — MATRIZ HTTP COMPLETA (2026-06-23)

**La matriz HTTP está COMPLETA**: las cuatro celdas — **Go + HTTP**, **Python + HTTP
(FastAPI)**, **Node + HTTP (Fastify)** y **Rust + HTTP (axum)** — están certificadas
con corrida real. Cada lenguaje generable soporta AMBOS transportes (gRPC y HTTP). La
matriz `supportedProtocolByLanguage` (en `core/services/migration/domain/domain.go`) es
el single-source-of-truth; el resto del codebase (prompts, assembler, worker) está en
lockstep con ella. Ya no quedan celdas HTTP pendientes.

### Celdas HTTP certificadas
- **Go + HTTP** — router REST nativo, sin grpc-gateway. Certificada.
- **Python + HTTP (FastAPI)** — app FastAPI + uvicorn + motor/pymongo + pydantic;
  el `.proto` con `google.api.http` sigue siendo el contrato autoritativo del que se
  deriva `docs/openapi.yaml`. Assembler `isPythonHTTP()` excluye el bootstrap del
  servidor gRPC (`grpc.server`/`add_*Servicer_to_server`) y los `*_pb2_grpc.py`.
  Prompt: `docs/prism/milton-prism-service-generator-prompt-python-http.md`.
  **Certificada (2026-06-23)** con mig 28 (flask-realworld@master, monolito):
  worker `profile=python protocol=http exitCode=0 gatesPassed=true cost=5.1753`,
  deliverable compila (`python -m compileall` exit 0) e importa, 26 tests verdes,
  `GET /v1/users → 200`, openapi scopeado al servicio (plataforma=0).

- **Node + HTTP (Fastify)** — app Fastify (rutas registradas + handlers REST) como
  único entrypoint; sin servidor gRPC (`@grpc/grpc-js` `new Server()`/`addService`) ni
  stubs `*_grpc_pb`; el `.proto` con `google.api.http` sigue siendo el contrato
  autoritativo del que se deriva `docs/openapi.yaml`. Assembler `isNodeHTTP()` excluye
  el bootstrap gRPC y los stubs `*_grpc_pb`. Prompt:
  `docs/prism/milton-prism-service-generator-prompt-node-http.md`. Gate de build: `tsc`.
  **Certificada (2026-06-23)** con mig 34 (flask-realworld@master, microservicios,
  serviceFilter=[user]): worker `profile=node protocol=http exitCode=0
  gatesPassed=true` → READY; deliverable Fastify (0 `new Server()`/`addService`),
  `npm install` + `npx tsc --noEmit` exit 0 en el ZIP; openapi plataforma=0.

- **Rust + HTTP (axum)** — app axum (`axum::Router` + handlers REST sobre tokio) como
  único entrypoint; SIN servidor gRPC tonic (`tonic::transport::Server`/`add_service`) ni
  `infrastructure/grpc/` ni codegen de servidor tonic-build; el `.proto` con
  `google.api.http` sigue siendo el contrato autoritativo del que se deriva
  `docs/openapi.yaml`. Assembler `isRustHTTP()`/`isRustGRPCArtifact()` excluye el
  bootstrap tonic y el dir `infrastructure/grpc/`. Prompt:
  `docs/prism/milton-prism-service-generator-prompt-rust-http.md`. Gate de build:
  `cargo build`. **Certificada por CONTENIDO (2026-06-23)** con **mig 38**
  (flask-realworld@master, **monolito**, servicio `app`): worker `service=app
  profile=rust protocol=http exitCode=0 gatesPassed=true` → READY; openapi ensamblado
  (protos=3). Evidencia de cierre:
    - **R3 deliverable que FUNCIONA** — `GET /v1/migrations/38:downloadDeliverable`
      → **200 `application/zip`** (`deliverable-38.zip`, 26 305 B, **31 archivos**).
      ZIP = workspace axum (`core/Cargo.toml` workspace + `core/services/app/` con
      `main.rs` que hace `axum::serve(listener, app)` + `routes.rs` con `axum::Router`
      y rutas REST `/v1/articles`, `/v1/tags`, …), `core/services/app/.env.example`,
      `core/shared/`, `protobuf/proto/` + `docs/openapi.yaml`. **0**
      `tonic::transport::Server`/`add_service` en todo el árbol. **Sin `target/`,
      `.cargo/`, `registry/` ni `.rlib/.rmeta`** en el ZIP. **BUILD REAL:**
      `docker run --rm -v <core>:/w -w /w -e CARGO_HOME=/tmp/cargo-home
      milton-prism-generation-agent:latest cargo build` → **exit 0** (`Finished dev
      profile … in 1m04s`; `app-service` + `shared` compilan, axum/mongodb/tower
      resueltos). Nota: con el `CARGO_HOME` por defecto del image (`/usr/local/cargo`,
      read-only para `prism`) `cargo build` falla con `Permission denied` al bajar las
      deps HTTP (axum/tower) que NO están en el pre-warm gRPC — es exactamente la causa
      raíz del DEFECT 4 (ver abajo): el agente reubicó `CARGO_HOME` dentro del workspace
      y se filtró todo el registry.
    - **R4 openapi scopeado** — `docs/openapi.yaml` de mig38 sólo el servicio generado
      (ArticleService / ProfileService / UserService / TagService); **CERO** plataforma
      (0 Migration/Identity/Analysis/Repository/Generation).
    - **R5 sin regresión** — `POST /v1/migrations/38:generationArtifacts` → **200**
      (88 KB, 2 servicios, 28 archivos) tras purgar el bloat; `:downloadDeliverable`
      GET de mig19/21/23/24/28/34 → **200** (zips válidos). `go build ./...` +
      `go test ./core/services/migration/... ./core/worker/generation/...` verdes.

  (mig35 era el intento Rust+HTTP en `@release`; quedó CANCELLED y soft-deleted —
  ver Limpieza. La celda certificada es mig38.)

MIG109 (`Failure_Unsupported_Protocol`, mensaje genérico) ahora sólo dispara para un
lenguaje no generable o un transporte desconocido; ninguna celda (lenguaje × HTTP) lo
dispara ya.

### DEFECT 4 — bloat del registry de Cargo en los artefactos Rust — ARREGLADO (2026-06-23)
- **Síntoma:** mig38 (Rust+HTTP, READY) persistió **8580** artefactos para el servicio
  `app`; un servicio axum tiene ~28 fuentes reales. Conteo por tipo (Mongo
  `generation_file_artifacts`, migration_id=38): **8552** bajo `.cargo/` (registry de
  crates: `.cargo/registry/src/…/<crate>/…` = fuentes descargadas, `index/`, locks),
  **0** bajo `target/` (el filtro de `target` ya funcionaba), **28** fuentes reales
  (`.rs`/`.toml`/`.proto`/`docs/openapi.yaml`/`.env.example`).
- **Causa raíz:** el agente corre `cargo build` dentro del workspace. El `CARGO_HOME`
  pre-warmeado del image (`/usr/local/cargo`) es read-only para el usuario `prism`
  (`chmod a+rX`), y el pre-warm sólo sembró el set gRPC (tonic/prost/…), NO las deps
  HTTP (axum/tower). Al necesitar bajarlas, cargo no puede escribir el registry
  compartido → el agente reubicó `CARGO_HOME` dentro del workspace (`$HOME/.cargo`), de
  modo que TODO el registry (índice + cada crate fuente) se materializó en el workspace;
  `diffFiles` lo vio como "nuevo" y `captureArtifacts` lo persistió. El filtro de
  exclusión del colector sólo tenía `target` (no `.cargo`/`registry`).
- **Arreglos (2 capas):**
  1. **Colector** (`core/worker/generation/infrastructure/agent/artifacts.go`):
     `artifactExcludeDirs` ahora incluye `.cargo`, `.rustup`, `.fingerprint`
     (segment-based); `isExcludedArtifactPath` también dropea sufijos `.rlib/.rmeta/.rs.bk`
     y los locks `.package-cache`/`CACHEDIR.TAG`. **No** se excluye un segmento `registry`
     pelado (lo cubre `.cargo`) para no romper un servicio legítimamente llamado
     `registry`. Test nuevo: `TestCaptureArtifacts_ExcludesCargoHomeRegistry` (mantiene
     `rust/services/registry/…`, dropea `.cargo/registry/src/…`).
  2. **Assembler** (`core/services/migration/application/assembler/assembler.go`):
     `isCargoBuildArtifact` (defensa en profundidad, ya invocado en el guard `isRust()`)
     ahora también matchea `.cargo`/`.rustup`/`.fingerprint` por segmento y
     `.rlib/.rmeta/.rs.bk` por sufijo, para que cualquier artefacto pre-fix ya
     persistido NO entre al ZIP. Test extendido: `TestAssemble_RustProfile`.
- **Purga de datos:** se borraron del DB los 8552 artefactos `.cargo` de mig38
  (deleteMany por segmento `.cargo`/`.rustup`/`.fingerprint`/`target` + sufijos
  compilados); quedaron las **28** fuentes reales. Esto resolvió además el
  `:generationArtifacts` que devolvía **ResourceExhausted** (respuesta gRPC de 96 MB por
  el conteo, no por UTF-8): el saneo UTF-8 (`service.go` DEFECT 3) ya existe y funciona
  (vacía el contenido no-UTF8 sin romper el marshal), pero el problema era el TAMAÑO/
  conteo del bloat, no UTF-8. Post-purga: `:generationArtifacts` → 200 (28 archivos).
- Redeploy: `infra/build.sh generation-worker migration-services` → `compose up --build
  -d` de ambos (el deliverable se ensambla en `migration-services`, el colector vive en
  `generation-worker`).

### DEFECT 5 (E10) — `.proto` bajo `core/services/` en el deliverable Rust gRPC — ARREGLADO (2026-06-23)
- **Síntoma:** mig23 (Rust+gRPC) shippeaba **16** `.proto` bajo
  `core/services/user/proto_include/google/…` (WKT de `google.protobuf` + anotaciones
  `google.api`). `core/services/` es código fuente; NINGÚN `.proto` debe vivir ahí — los
  protos sólo viven en `protobuf/proto/`. El árbol canónico
  `protobuf/proto/milton_prism/…` ya estaba correcto; el bug era el vendoring per-servicio.
- **Causa raíz:** el `protoc` del agent image (`/usr/bin/protoc`, libprotoc 31.1) **no trae
  includes** (`/usr/include/google/protobuf` y `/usr/local/include` vacíos), así que el
  agente vendoriza los protos google que `tonic-build` necesita para resolver `import`s en
  `rust/services/<svc>/proto_include/google/…` y añade ese dir como segundo include de
  `compile_protos`. El rename `rust/`→`core/` del assembler lo convierte en
  `core/services/<svc>/proto_include/…`. La opción "usar el include estándar de protoc"
  NO es viable con este image (protoc no trae nada).
- **Arreglo (assembler, sin regen):**
  `core/services/migration/application/assembler/assembler.go` — nuevo guardrail
  `relocateRustVendoredProtos` (invocado en el branch `isRust()` de `Assemble`, antes del
  rename): reubica todo `rust/services/<svc>/proto_include/<import-path>` a la ruta canónica
  top-level `protobuf/proto/<import-path>` (el sufijo tras `proto_include/` ES el string de
  `import` de protoc, p.ej. `google/protobuf/timestamp.proto`), dedup entre servicios, borra
  las copias per-servicio, y `stripProtoIncludeFromBuildRs` reescribe cada `build.rs`
  (quita el binding `let vendored_includes = "proto_include";` y colapsa el slice de includes
  a `&[proto_root]`). Las deps google ahora resuelven vía el include root `protobuf/proto`
  que `build.rs` ya pasaba. Test nuevo: `TestAssemble_RustGRPC_RelocatesVendoredProtos`
  (0 `.proto` bajo core/services/, reubicación + dedup multi-servicio, build.rs reescrito).
- **Cobertura:** el guardrail es Rust-only; Go/Python/Node ya no metían `.proto` bajo
  `core/services/` (verificado mig24/mig28/mig38). Rust HTTP (axum) usa serde structs sin
  tonic-build, no vendoriza, pero el guardrail lo cubriría igual.
- Redeploy: `infra/build.sh migration-services` → `compose up --build -d --force-recreate`
  (el deliverable se ensambla en `migration-services`; no requiere regen — cert por
  re-descarga).
- **Certificación (gateway :8083, dev@prism.local 10004; EdDSA bearer):**
  - **R1** mig23 re-descarga (`GET :downloadDeliverable`): **0** `.proto` bajo
    `core/services/`; los 16 google + 2 milton_prism viven sólo en `protobuf/proto/`;
    `build.rs` reescrito a `&[proto_root]`.
  - **R2** `cargo build` del ZIP extraído (`docker run … milton-prism-generation-agent:latest
    cargo build` en `core/services/user`): **exit 0** (compila build.rs/tonic-build + crate
    `user-service` + `shared`).
  - **R3** mig38 (Rust HTTP) re-descarga: **0** `.proto` bajo `core/services/`. Go (mig24) y
    mig28 sin regresión (0 cada uno).
  - **R4** `go build ./...` + `go test ./core/services/migration/...` verdes.

### Go + gRPC + monolito — CERTIFICADO con mig45 (2026-06-23)
Celda **Go + gRPC + monolito** (1 servicio `app`), READY. Deliverable verificado por
**build real**: `GET /v1/migrations/45:downloadDeliverable` → 200 ZIP; extraído y
`go build ./...` → **exit 0**. Estructura coherente: top-level
`core/ docs/ go.mod go.sum Makefile pkg/ protobuf/`; **0** `.proto` bajo `core/services/`;
protos sólo en `protobuf/proto/`; servicio `app` hexagonal con server gRPC;
`docs/openapi.yaml` scopeado (plataforma=0); `:generationArtifacts` 200.

**DECISIÓN del gateway — OPCIÓN A (gateway EMBEBIDO intencional).** Un monolito gRPC
expone REST vía un grpc-gateway **in-process en el MISMO binario**: `pkg/gateway/`
(`gateway_with_service.go`, `rest.go`, `handlers/`, `common/error/`) es la librería del
gateway, y `core/internal/svc/build_server_group.go` la cablea llamando
`gateway.StartGatewayWithService(...)`. Es un solo deployable sirviendo gRPC + REST → el
subtree `pkg/gateway/` es CORRECTO en esta celda y NO se excluye. La regla
`useApiGateway = (topology != Monolith) && (transport == gRPC)` ⇒ `false` en monolito sólo
suprime el **entrypoint standalone** `api-gateway/cmd/...` (el gateway separado que
frontea N microservicios), que `download_deliverable.go` ya calcula bien: el ZIP de mig45
NO trae `api-gateway/` (verificado), pero SÍ trae la librería embebida. No es un leak del
gateway; es el patrón válido de un monolito gRPC con REST in-process.

**Dos defectos reales de COMPILACIÓN que SÍ bloqueaban `go build` (arreglados):**
1. **Clientes gRPC de plataforma filtrados** — el esqueleto enviaba
   `core/shared/grpc_client_sdk/grpc_billing_client.go` y `grpc_migration_client.go`, que
   importan stubs de plataforma (`pkg/pb/gen/.../services/{billing,migration}/v1`) que
   `skipDir` poda de TODO deliverable ⇒ `package … not in std`. La lista de exclusión de
   `isSkeletonFile` ya dropeaba los 3 análogos (`analysis/identity/repository`) pero
   faltaban billing y migration. **Fix:** añadidos a la exclusión base en
   `assembler.go::isSkeletonFile` (aplica a TODAS las celdas Go; se simplificó el bloque
   `isGoHTTP` que duplicaba la exclusión de billing). Ninguno es referenciado por código
   generado ni por el gateway/internal que sí se envía.
2. **`message_error.go` generado con shape obsoleto** — el agregador `__pipeline__`
   (`error_aggregator.go::buildMessageErrorGo`) emitía un `ErrorMessage` SIN el campo
   `Code` ni `looksLikeErrorCode`, pero el esqueleto actual
   `pkg/gateway/handlers/error.go` referencia `errorMessage.Code` (feature de gateway
   error-codes, commit 29e02f4) ⇒ `unknown field Code`. **Fix:** `buildMessageErrorGo`
   actualizado para emitir el body canónico (campo `Code`, bloque `emittedCode` y
   función `looksLikeErrorCode`), en lockstep con
   `pkg/gateway/common/error/message_error.go`. Afecta a TODA celda gRPC que envíe el
   gateway. El artefacto YA persistido de mig45 (Mongo `generation_file_artifacts`) se
   regeneró in-place con el shape corregido (no requirió re-correr el agente).

**Evidencia (C1–C3):**
- **C1:** ZIP re-descargado tras el fix → `go build ./...` **exit 0** (sin errores).
  `grpc_client_sdk/` sólo tiene `builder.go`; `message_error.go` con campo `Code`;
  `pkg/gateway/` presente (embebido); sin `api-gateway/`; 0 `.proto` en `core/services/`.
- **C2:** decisión = **Opción A** (documentada arriba); el deliverable compila con el
  gateway embebido coherente.
- **C3:** repo `go build ./...` exit 0; `go test ./core/services/migration/...
  ./core/worker/generation/application/...` verdes; mig24 (Go+HTTP+mono) re-descargada y
  `go build ./...` exit 0 (sin regresión; su `pkg/gateway` sigue trimmed a `common/error`).
- **Deploy:** `infra/build.sh migration-services` + `compose up --build migration-services`.

### Inventario final de celdas (verificado 2026-06-23)
Migraciones ACTIVAS (`GET /v1/migrations` → `[19,21,23,24,28,34,38,45]`), todas READY:
| mig | lenguaje | protocolo | topología | celda |
|-----|----------|-----------|-----------|-------|
| 19  | Python   | gRPC      | micro     | Py-gRPC |
| 21  | Node     | gRPC      | micro     | Node-gRPC |
| 23  | Rust     | gRPC      | micro     | Rust-gRPC |
| 24  | Go       | HTTP      | monolito  | Go-HTTP |
| 28  | Python   | HTTP      | monolito  | Py-HTTP |
| 34  | Node     | HTTP      | micro     | Node-HTTP |
| 38  | Rust     | HTTP      | monolito  | **Rust-HTTP** (cierre por contenido) |
| 45  | Go       | gRPC      | monolito  | **Go-gRPC-monolito** (compila, `go build` exit 0) |

- **Limpieza:** mig35 (Rust+HTTP, CANCELLED) y mig36 (Rust+HTTP, FAILED) ya estaban
  soft-deleted (`delete_time` poblado) — excluidos del listado activo. DELETE explícito
  → 404 (ya tombstoned). Cleanup confirmado.
- **Go-gRPC:** **CUBIERTO por mig45 (2026-06-23)** — celda Go+gRPC+monolito viva y READY,
  deliverable que **COMPILA** (`go build ./...` exit 0); ver sección dedicada abajo. Las
  únicas migraciones Go-gRPC previas (mig25, mig26) están CANCELLED. La matriz gRPC ahora
  conserva las cuatro lenguajes con celda gRPC viva: Go (45), Py (19), Node (21), Rust (23).

### Las 4 combinaciones protocolo × topología tienen ≥1 cert (2026-06-23)
Con mig45 (Go-gRPC-monolito) la matriz `{gRPC,HTTP} × {monolito,micro}` queda con al
menos una celda certificada en cada cuadrante:
| | monolito | micro |
|--|----------|-------|
| **gRPC** | **mig45 (Go)** | mig19/21/23 (Py/Node/Rust) |
| **HTTP** | mig24/28/38 (Go/Py/Rust) | mig34 (Node) |

### Saneo del mensaje de error de generación — HECHO (2026-06-23)
- El `reason` de un fallo de gates ya NO expone el blob crudo de Claude Code
  (`total_cost_usd`/`session_id`/`usage`/`modelUsage`). `SanitizeFailureReason`
  (`core/worker/generation/domain/failure_reason.go`) reduce el blob a un mensaje
  técnico limpio (≤200 chars, sin JSON ni claves sensibles); el blob completo se loguea
  server-side (applog) para diagnóstico. Aplicado en el invoker
  (`claude_agent_invoker.go`) y al persistir en el pipeline
  (`pipeline.go`); `RawFailureReason` (interno) se conserva sólo para la detección de
  errores transitorios (rate-limit). Backfill del `reason` crudo de mig30 → mensaje
  limpio en Mongo. Tests: `failure_reason_test.go` (sobre el blob real de mig30).

Para habilitar una nueva celda HTTP hay que, en lockstep: (1) añadir
`TransportHTTP` al set del lenguaje en `supportedProtocolByLanguage`; (2) crear el
prompt HTTP del lenguaje y referenciarlo en `generatorPromptRef` (service) +
`profileAndPromptForLanguage` (worker); (3) inyectar la sección de transporte en
`promptProfileBindings`/`transportSection` (worker); (4) decidir el comportamiento
del assembler para esa celda; (5) certificar con una corrida real.

### NATS
- El valor `TRANSPORT_NATS=3` del enum `Transport` quedó **`reserved 3`** en el
  proto (fuera del set ofrecido). Mensajería sigue out-of-scope.

## Symlink OpenAPI del panel — REALIDAD (verificado 2026-06-23)
- El symlink **EXISTE**: `backend/milton-prism-panel` (en el árbol canónico:
  `milton-prism-panel`) → `/mnt/usr/src/desarrollo/js/RustLangLatam/milton-prism-panel`.
- `protobuf/buf.docs.gen.yaml` escribe `openapi.yaml` **directamente** en
  `../milton-prism-panel` (la raíz del panel) vía el symlink. NO hay drift: tras
  `buf generate --template buf.docs.gen.yaml`, el `panel/openapi.yaml` queda
  actualizado solo. Confirmado: el enum `interServiceTransport` del panel pasó a
  `[TRANSPORT_UNSPECIFIED, TRANSPORT_GRPC, TRANSPORT_HTTP]` (sin NATS) con la nueva
  descripción.
- `npm run gen:api` (cliente TS del panel) lo corre el agente frontend; NO se tocó
  desde backend.

## Unicidad de migración: clave {repo,branch,commit,topology,language,protocol} — RESUELTO (2026-06-23)
- RESUELTO: la clave de unicidad se extendió a las 6 dimensiones
  `{repository_id, source_branch, commit_sha, topology, language, protocol}`
  (protocol = `inter_service_transport` canonicalizado, UNSPECIFIED⇒GRPC). El doc de
  migración persiste `language` y `protocol` denormalizados (junto a `topology`) en
  `migrationToDoc`; el índice único parcial pasó de
  `uniq_repo_branch_commit_topology` (4 campos) a
  `uniq_repo_branch_commit_topology_language_protocol` (6 campos), con el mismo
  `partialFilterExpression` sobre `commit_sha`. La creación de índices es idempotente
  (drop del viejo en el constructor del repo → create del nuevo). El bloqueo MIG223
  sigue siendo DB-driven: el stamping de `commit_sha` en la analysis-worker colisiona
  contra el índice de 6 dimensiones. Resultado: celdas distintas de la matriz
  {lenguaje × protocolo × topología} sobre el mismo repo/branch/commit se permiten
  todas; sólo dos intentos idénticos en las 6 dimensiones colisionan (MIG223).
- Evidencia: tests `TestSummaryWriter_CommitBlock` + `TestSummaryWriter_MatrixCellsDistinct`
  (verdes contra Mongo vivo); índice de 6 campos confirmado en
  `db.migrations.getIndexes()`; el de 4 campos ya no existe.

### (histórico) clave {repo,branch,commit,topology} — INSUFICIENTE (verificado 2026-06-23)
- La clave de unicidad actual `{repository, branch, commit, topology}` NO incluye
  **lenguaje** ni **protocolo**. Esto permitió crear DOS migraciones
  Go+HTTP+monolito sobre el MISMO `flask-realworld-example-app@master` sin colisión
  (la dup ~mig27 se creó al probar el guard G1). Mientras que `topology` sí entra en
  la clave, `target.language` y `target.protocol` quedan fuera, de modo que el mismo
  repo/branch/topología con distinto lenguaje/protocolo se trata como duplicado-no
  (se permite), pero el MISMO lenguaje/protocolo/topología tampoco colisionó.
- PROPUESTA: extender la clave de unicidad a
  `{repository, branch, commit, topology, language, protocol}` para que cada celda
  (lenguaje × protocolo × topología) sea única por repo/branch/commit, y dos intentos
  idénticos de la MISMA celda sí colisionen (evitando duplicados de prueba como el
  mig27). Evaluar impacto en el índice único de Mongo
  (`mongo_migration_repository`) y en el chequeo de duplicados del service layer
  (`CreateMigration`).
- No bloqueaba ninguna certificación; era higiene de datos / DX. (resuelto arriba)

## Eliminar/cancelar análisis y migraciones + multi-migración por análisis (2026-06-23)

### BACKEND — HECHO (certificado C1–C6 por gateway :8083)
- **Proto** (cadena regenerada: `buf lint` + `buf generate` go.gen + docs.gen → `panel/openapi.yaml` por symlink):
  - `analysis_service.proto`: nuevos RPC `CancelAnalysis` (`POST /v1/analysis_summaries/{identifier}:cancel` → `AnalysisSummary`) y
    `DeleteAnalysisSummary` (`DELETE /v1/analysis_summaries/{identifier}` → `google.protobuf.Empty`); import de `google/protobuf/empty.proto`.
  - `types/analysis/v1/analysis.proto`: `ANALYSIS_STATE_CANCELLED = 5`.
  - `types/migration/v1/migration.proto`: `MigrationsFilter.analysis_summary_id` (field 5, optional) — habilita la multi-migración y el guard.
- **Análisis**: `repo.UpdateState(ctx,id,state)`; errores `ANL207 Failure_Analysis_Has_Live_Migrations` y
  `ANL208 Failure_Invalid_State_Transition` (ambos → FailedPrecondition, en gateway error map);
  `isTerminalAnalysisState = {COMPLETED, FAILED, CANCELLED}`;
  `CancelAnalysis` (rechaza terminal → ANL208; si no, UpdateState→CANCELLED);
  `DeleteAnalysisSummary` (rechaza no-terminal → ANL208; guard de migraciones vivas vía nuevo port
  `MigrationClient.CountLiveMigrationsByAnalysis` → ANL207; si OK, SoftDelete).
- **Guard cross-service**: nuevo `grpc_client_sdk.MigrationGrpcClient` + adapter `MigrationClientAdapter`
  (forwardea el bearer token; cuenta vía `ListMigrations(filter{analysis_summary_id, states=ACTIVOS}, pageSize=1)`
  leyendo `pagination.total_size`). Estados ACTIVOS = no-terminales (PENDING..READY). Config
  `[grpcServices.migrationServices]` ya presente en `analysis-services/config.toml`; wire en `analysis/wire.go`.
  Sin migration client el guard **degrada CERRADO** (delete rechazado, ANL500) para no orfandar migraciones.
- **Migración**: `MigrationsFilter.analysis_summary_id` aplicado en el `List` del mongo repo (filtro proto→domain es type-alias, pasa directo); `ListMigrations` ya rellena `pagination.total_size`.
- **Worker cancel cooperativo** (PENDIENTE-4): YA satisfecho — el `Write`/`MarkAnalysisFailed`/`MarkAwaitingRootSelection`
  del analysis-worker ya filtran por `state==RUNNING`, así que un CANCELLED (state=5) no se sobrescribe (soft-cancel).
- **Tests** (nuevos, verdes): `service_test.go` (Cancel/Delete: success, terminal-rejected, live-migrations→ANL207,
  non-terminal→ANL208, degrade-closed) y `analysis_handler_test.go` (handlers + ownership + FailedPrecondition).
- **Evidencia E2E** (gateway :8083, dev@prism.local):
  - C1: cancel RUNNING (id 10028, re-run force) → CANCELLED 200; cancel terminal → ANL208.
  - C2: DELETE análisis 10026 COMPLETED con migs vivas (19/24/28 READY) → ANL207.
  - C3: DELETE análisis terminal 10028 (CANCELLED, 0 migs vivas) → 200/Empty; GET → 404 (soft-deleted).
  - C4: `ListMigrations(filter.analysisSummaryId=10026)` → 3 (19/24/28); `=10042` → 2 (23/38).
  - C5: DELETE migración READY 19 → MIG202 (terminal-guard); `:cancel` migración no-terminal (mig 39 disposable) → CANCELLED 200.
  - C6: `go build ./...` + tests análisis/migración verdes; 7 celdas vivas (19/21/23/24/28/34/38) siguen READY/200 (total=7).
  - Limpieza: mig 39 disposable borrada; análisis 10028 quedó soft-deleted (consumido en C1/C3, no era una de las 7 celdas vivas).

### PASO SIGUIENTE — FRONTEND WIRING (no hecho aquí; es del agente frontend)
- `npm run gen:api` para regenerar el api-client TS desde el `panel/openapi.yaml` ya actualizado (NO corrido aquí — es del frontend).
- Botones + modales de confirmación reusando `ConfirmModal` ya existente; estados de ciclo de vida via `lifecycleStates.ts`
  (añadir `ANALYSIS_STATE_CANCELLED`); mapas de error i18n para ANL207/ANL208 (textos ya en el gateway error map).
- UI multi-migración: listar todas las migraciones de un análisis via `ListMigrations?filter.analysisSummaryId=<id>`.
- La fundación frontend reutilizable (ConfirmModal, error maps, i18n) YA existe — solo wiring.
