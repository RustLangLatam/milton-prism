# Milton Prism — Tareas: Motor de Descomposición (núcleo determinista)

Copia y pega **un bloque a la vez**, en orden. Verifica entre cada uno. Implementa el **núcleo determinista** del motor de descomposición (sin LLM); los huecos LLM quedan como stubs marcados.

**Referencia obligatoria:** `docs/prism/milton-prism-decomposition-engine-spec.md` (colócalo en `docs/prism/` primero), más el Canon. El motor corre en el worker como la etapa `DESIGNING`, igual de async/idempotente que el análisis.

**Objetivo de validación (lo sabés de antemano):** sobre `gothinkster/flask-realworld-example-app` (Conduit, 28 nodos / 50 aristas), el motor debe clasificar `conduit.database`/`extensions`/`settings`/`utils`/`app`/`autoapp`/`exceptions`/`commands` como **infraestructura compartida** (no servicios), y producir **~3 servicios**: identity (`conduit.user.*`), profile (`conduit.profile.*` → dep identity), article (`conduit.articles.*` → deps identity, profile). Esa es la prueba de aceptación.

---

## Gate block (correr después de cada tarea, con CGO_ENABLED=1)

```
buf lint
go build ./...
go vet ./...
go test ./core/worker/analysis/... ./core/services/migration/... ./core/services/analysis/...
```

---

## Tarea D1 — Trigger DESIGNING + carga de grafo + detección de infraestructura

```
Tu tarea es solo esta. No toques otros motores ni el frontend.

Lee docs/prism/milton-prism-decomposition-engine-spec.md (secciones 1, 4 stage 1-2, 5).

1. Trigger: cuando el análisis completa y la migración pasa a DESIGNING, encolá un job de
   descomposición en Asynq (mismo patrón que StartMigration→RunAnalysis). El worker tiene un
   handler nuevo "decompose" que arranca el pipeline de descomposición para esa migración.
   Idempotente: re-correr lee el estado persistido, no duplica.
2. Stage 1 — GraphLoader: cargá el dependency_graph + los blueprints desde el AnalysisSummary
   de la migración (puerto GraphLoader, adaptador que lee de Mongo).
3. Stage 2 — InfraDetector: separá los módulos de infraestructura compartida de los de dominio.
   Heurística: fan-in desde ≥2 clusters/blueprints distintos Y sin identidad de dominio propia.
   Puerto InfraDetector con adaptador determinista.
4. Por ahora, el pipeline termina acá: logueá la clasificación (infra vs dominio) y NO avances
   el estado todavía.
5. Validación: corré una migración sobre Conduit hasta DESIGNING y mostrame la clasificación.
   conduit.database/extensions/settings/utils/app/autoapp/exceptions/commands deben quedar como
   INFRA; los módulos de user/profile/articles como DOMINIO. Tests con un grafo fixture que
   reproduzca esa forma.

Respetá la regla de dependencias del Canon (nada de Mongo/parsing en application). Gate block.
```

---

## Tarea D2 — Clustering (community detection) + caracterización

```
Tu tarea es solo esta. No toques contract derivation ni el frontend.

Lee la sección 4 (stage 3-4) y 5 del spec.

1. Stage 3 — SemanticClusterer (PUERTO, desde el día uno):
   - Adaptador determinista (LIVE): community detection (Louvain o label propagation) sobre la
     proyección no-dirigida ponderada del grafo de DOMINIO (sin la infra de D1), SESGADO por
     blueprints (módulos del mismo blueprint prefieren fuertemente el mismo cluster). Devuelve
     clusters + un score de modularidad.
   - Adaptador LLM (HUECO): stub que levanta "no implementado". NO lo implementes.
   - Fallback: si la modularidad está por debajo de un umbral (grafo insuficiente / un cluster
     gigante), NO falles ni adivines: producí la partición determinista de mejor esfuerzo y
     marcá un flag de baja confianza en el resultado.
2. Stage 4 — caracterización: por cada cluster, derivá nombre de servicio (del blueprint /
   path dominante), recursos que posee (los modelos de dominio del cluster), y deps inter-servicio
   (las aristas que cruzan fronteras de cluster). Asigná un prefijo de error único por servicio
   (PrefixAllocator simple para v1, marcado como el punto de integración con el registry del
   orquestador — no inventes colisiones).
3. Validación: sobre Conduit, debe producir ~3 clusters → identity (user), profile, article, con
   modularidad ALTA (no dispara el fallback). Las deps: profile→identity, article→identity+profile.
   Tests con el grafo fixture de Conduit verificando la partición esperada y la asignación de deps.

Gate block.
```

---

## Tarea D3 — Derivación de contratos (.proto determinista, Flask/SQLAlchemy)

```
Tu tarea es solo esta. No toques el ensamblado del plan ni el frontend.

Lee la sección 4 (stage 5) del spec.

1. Stage 5 — ContractDeriver (PUERTO, adaptador per-framework Flask/SQLAlchemy LIVE; otros frameworks HUECO):
   - De los modelos SQLAlchemy de cada cluster, derivá los mensajes de recurso .proto, AIP-compliant:
     identifier (uint64), state enum, sufijos _time, soft-delete (delete_time/purge_time). Mapeo
     campo de modelo → campo proto. ESTO ES DETERMINISTA.
   - De las rutas/blueprints Flask, derivá los RPCs: las rutas CRUD mapean a métodos estándar
     (Get/List/Create/Update/Delete) de forma determinista. Las rutas NO-CRUD (custom) se MARCAN
     como pendientes (hueco LLM/humano), NO se inventan — dejá un comentario claro en el .proto
     derivado indicando la ruta original sin mapear.
   - Escribí los .proto derivados al workspace de la migración (son artefactos generados, no van
     al árbol proto del repo).
2. Validación: sobre Conduit, debe derivar .proto para identity (User), profile (Profile),
   article (Article, Comment, Tags). Verificá que los recursos tienen identifier/state/_time y que
   las rutas CRUD de Conduit mapearon a métodos estándar. Tests con modelos/rutas fixture.

Respetá AIP del Canon en los .proto derivados. Gate block.
```

---

## Tarea D4 — Ensamblado del plan + DB compartida + avanzar a AWAITING_APPROVAL

```
Tu tarea es solo esta. No toques el frontend.

Lee las secciones 2, 4 (stage 6-7) y 6 del spec.

1. Stage 6 — ownership de datos: asigná cada recurso a su servicio. Declará shared_database=true
   en cada boundary spec. Listá las foreign keys cruzadas como deuda de consistencia diferida
   (en Conduit: article.author→user, comment.author→user, follow→user). Insertá el marcador
   // TODO: per-service data ownership + cross-service consistency en los specs generados.
2. Stage 7 — PlanWriter: llená el RestructurePlan (proto existente en migration.proto):
   ProposedService[] (name, error_prefix, owned_resources, inter_service_deps) + rationale.
   Escribí también las boundary specs (YAML que consume el generador) al workspace. Seteá
   Migration.plan y avanzá el estado DESIGNING→AWAITING_APPROVAL. Si el flag de baja confianza
   de D2 está activo, marcalo en el plan.
3. Validación end-to-end: una migración sobre Conduit debe ir de DESIGNING a AWAITING_APPROVAL
   con un RestructurePlan real de ~3 servicios, shared_database=true declarado, y las FKs cruzadas
   listadas. Mostrame el RestructurePlan resultante completo. Tests del ensamblado + la transición.

Gate block.
```

---

## Tarea D5 — Frontend: pantalla de aprobación del plan

```
Tu tarea es solo esta. Frontend.

En MigrationDetailPage, para el estado AWAITING_APPROVAL, reemplazá el placeholder por la vista
de revisión del plan (la pantalla restructure_plan_review del export de Stitch):
- Llamá getMigration y renderizá el RestructurePlan real: servicios propuestos (nombre, prefijo,
  recursos que posee, deps inter-servicio), el rationale, y un diagrama/lista de las deps.
- Banner de advertencia visible: shared_database=true significa que esto NO es todavía
  microservicios independientes (monolito distribuido hasta separar datos). Mostrá las FKs cruzadas.
- Si el plan tiene el flag de baja confianza, mostrá la advertencia de "descomposición basada en
  grafo con baja confianza; revisión humana recomendada".
- Barra de decisión: "Aprobar y generar" (→ approveDesign con approved=true, lleva a GENERATING) y
  "Rechazar" (→ approveDesign con approved=false).
- vite build limpio.

Probá contra una migración de Conduit que esté en AWAITING_APPROVAL y reportá qué se renderiza.
```

---

## Reglas de la corrida

- Una tarea por bloque. Gate block (CGO_ENABLED=1) entre cada una.
- Conduit es el objetivo de validación en cada tarea: ya sabés la respuesta correcta (3 servicios + infra compartida), así que cada tarea se valida contra eso.
- Huecos (adaptador LLM del clusterer, semántica de rutas no-CRUD, separación de datos por servicio, router multimodelo) = stub marcado que reporta, NUNCA adivina.
- Regla de dependencias del Canon: parsing/Mongo/SDKs solo en adaptadores de infraestructura.
- shared_database=true es deuda DECLARADA y visible, no silenciosa.
- 3 fallos seguidos en un gate → STOP note en docs/prism/decomposition-blockers.md y seguir.
```
