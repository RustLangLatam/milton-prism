# Milton Prism — Endurecer el Perfil Python: generar repository + migration

Copia y pega **un bloque a la vez**, en orden. Verifica entre cada uno.

**Qué es esto:** validación/endurecimiento del Perfil Python generando dos servicios más complejos que `identity` — `repository` (deps inter-servicio + stub git) y `migration` (máquina de estados + 3 deps inter-servicio + stub analysis). NO es construir la plataforma en Python; la plataforma es Go. Estos son la implementación de referencia del perfil, en el monorepo Python, junto al `identity` Python ya generado.

**Ventaja clave — hay verdad de terreno:** `repository` y `migration` ya existen en Go (mismos protos, mismo comportamiento). Las versiones Go son la **referencia**: las versiones Python deben comportarse igual (mismos RPCs, misma máquina de estados, mismos códigos de error, mismos chequeos de ownership), salvo las desviaciones idiomáticas legítimas del lenguaje. El lado Go **no se toca**.

Usa siempre: el Canon (`docs/prism/milton-prism-architecture-canon.md`), el Perfil Python (`docs/prism/milton-prism-python-profile.md`), y el prompt generador Python (`docs/prism/milton-prism-service-generator-prompt-python.md`).

---

## Gate block (correr después de cada tarea)

```
poetry install
buf lint
ruff check .
mypy --strict .
lint-imports
pytest -q
```

---

## Tarea PT1 — Transaction manager real (resolver deuda antes de migration)

```
Tu tarea es solo esta. No toques el lado Go. No generes servicios todavía.

Contexto: en el identity Python, el TransactionManager quedó wired como None porque las
transacciones de Motor requieren un replica set. migration SÍ necesita atomicidad real (su
máquina de estados escribe estado + plan juntos), así que resolvemos esto ahora.

1. Implementa MotorTransactionManager en shared/mongo_client (o donde viva el cliente Mongo),
   cumpliendo el puerto TransactionManager del Perfil Python (A.5): with_transaction(fn)
   abre una sesión Motor (start_session) y corre fn dentro de session.with_transaction(...).
   Mantén la degradación nil-safe: si el manager es None, corre fn sin transacción.
2. Documenta en docs/prism/python-dev-setup.md cómo correr MongoDB como replica set de un solo
   nodo en desarrollo (mongod --replSet rs0 + rs.initiate()), que es lo que habilita las
   transacciones en una sola instancia. Esto es setup de dev, no infra de producción.
3. Refactoriza el wire.py de identity para construir y wirear el MotorTransactionManager real
   (ya no None). identity sigue funcionando igual; solo gana atomicidad donde aplica.
4. Tests: con un MongoDB replica-set disponible (testcontainers configurado como replica set,
   o skip marcado si no hay uno), verifica que with_transaction hace commit en éxito y rollback
   en excepción. Si no hay replica set en el entorno de test, el test se marca skip con razón
   clara, NO se borra.

Corre el gate block.
```

---

## Tarea PT2 — Generar `repository` en Python

```
Tu tarea es solo esta. No toques el lado Go ni el identity Python (salvo lo ya hecho en PT1).

Usa docs/prism/milton-prism-service-generator-prompt-python.md como tus instrucciones. Lee el
Canon y el Perfil Python antes de generar. Genera el servicio repository en el monorepo Python.

Boundary spec:
  service: repository
  resources: [Repository]  (proto_type repository.v1.Repository, soft_delete: true)
  rpcs: [CreateRepository, GetRepository, ListRepositories, UpdateRepository, DeleteRepository,
         TestConnection, ListBranches, PushResult]
  store: mongodb
  needs_transaction: true   (usa el MotorTransactionManager de PT1)
  error_prefix: "REPO"      (mismo prefijo que la versión Go — NO reasignes)
  inter_service_deps: [identity]
  auth: required

Reglas específicas:
- La dependencia hacia identity es un PUERTO (Protocol) en application (UserValidator) con un
  adaptador cliente gRPC en shared/grpc_client_sdk (patrón del Perfil A.3). En tests se mockea.
- Las operaciones git (TestConnection, ListBranches, PushResult) son un PUERTO GitClient con un
  adaptador STUB (NoOpGitClient) que levanta un DomainError "no implementado" claramente marcado
  con TODO. NO implementes git real.
- credential_ref se guarda tal cual; nunca se devuelve en respuestas.
- Comportamiento equivalente al repository Go: mismos RPCs, mismos códigos REPO, mismo strip de
  credential_ref, misma validación de ownership.

Corre el gate block.
```

---

## Tarea PT3 — Generar `migration` en Python

```
Tu tarea es solo esta. No toques el lado Go ni los servicios Python previos.

Usa el prompt generador Python. Lee el Canon y el Perfil Python. Genera migration en el monorepo Python.

Boundary spec:
  service: migration
  resources: [Migration]  (proto_type migration.v1.Migration, soft_delete: true)
  rpcs: [CreateMigration, GetMigration, ListMigrations, DeleteMigration, StartMigration,
         ApproveDesign, CancelMigration]
  store: mongodb
  needs_transaction: true   (wirea el MotorTransactionManager real de PT1)
  error_prefix: "MIG"       (mismo prefijo que la versión Go — NO reasignes)
  inter_service_deps: [repository, identity, analysis]
  auth: required

Reglas específicas:
- Máquina de estados en application, idéntica a la versión Go:
  PENDING→ANALYZING→DESIGNING→AWAITING_APPROVAL→GENERATING→TESTING→READY→PUSHED, más FAILED/CANCELLED.
  Cada transición ilegal levanta un DomainError claro. StartMigration/ApproveDesign/CancelMigration
  son transiciones; este servicio NO ejecuta análisis/diseño/generación.
- Deps inter-servicio como PUERTOS (Protocol) con adaptadores: repository e identity son clientes
  gRPC en shared/grpc_client_sdk; analysis NO existe como servicio Python aún, así que su puerto
  (AnalysisClient) tiene un adaptador STUB (NoOpAnalysisClient) claramente marcado con TODO. Ojo:
  un stub que devuelve "válido" sin verificar es engañoso — que levante DomainError "no implementado"
  o se marque explícito, no que apruebe en silencio.
- Las operaciones que cambian estado + escriben plan/output van dentro de with_transaction (atomicidad).
- Comportamiento equivalente a la versión Go: mismas transiciones válidas e inválidas, mismo ownership,
  ListMigrations fuerza el filtro de owner para callers no-system.

Corre el gate block.
```

---

## Tarea PT4 — Paridad contra Go + reporte

```
Tu tarea es solo esta. No cambies código salvo para corregir divergencias que encuentres.

Compara los servicios repository y migration de Python contra sus contrapartes Go (que son la
referencia de comportamiento). Escribe docs/prism/python-parity-report.md con:

- Por servicio: confirma paridad de RPCs, de códigos de error (REPO*/MIG*), de la máquina de estados
  de migration (mismas transiciones válidas e ilegales), de los chequeos de ownership, y de los stubs
  (git, analysis) marcados igual que en Go.
- Lista las desviaciones Python→Go con su razón (como hizo el reporte de identity). Distingue
  desviaciones idiomáticas legítimas (async, Protocol, raise vs return) de divergencias de
  comportamiento (esas últimas son bugs: corrígelas).
- Estado de los gates y números de cobertura.
- Confirma explícitamente que tx real (PT1) está wired en migration y que las transiciones de estado
  son atómicas.

Corre el gate block una última vez.
```

---

## Guardrails de la corrida

- Una tarea por bloque. Gate block entre cada una. 3 intentos fallidos en un gate → STOP note en docs/prism/python-profile-blockers.md y seguir con la siguiente tarea independiente.
- A diferencia de identity, AQUÍ HAY REFERENCIA: ante una duda de comportamiento, mira cómo lo hace la versión Go y replica el comportamiento (no el mecanismo — el mecanismo es Python idiomático). Una divergencia de comportamiento respecto a Go es un bug, no una desviación.
- Mismos prefijos de error que Go (REPO, MIG). No reasignar.
- El lado Go no se toca para nada.
- mypy ignore_errors (si se usa) acotado SOLO a módulos que tocan stubs de terceros incompletas, nunca a domain/application.
```
