# Milton Prism — Tareas para Claude Code: completar Tier 1 del motor de análisis

Copia y pega **un bloque a la vez**, en el orden listado. Después de cada tarea, corre el bloque de verificación. No pases a la siguiente hasta tener verde la anterior.

El orden importa: **manifiestos → versiones → vulnerabilidades**, porque versiones y vulnerabilidades consumen la lista de dependencias `(ecosistema, paquete, versión)` que produce manifiestos.

---

## Verificación (correr después de cada tarea)

```
buf lint
buf generate
go build ./...
go vet ./...
go test ./core/services/analysis/... ./core/cmd/analysis-worker/...
```

Todo debe pasar antes de continuar.

---

## Tarea 1 — Contrato de merge del arreglo `technologies`

Pega esto primero. Establece cómo conviven los escritores del campo `technologies` antes de agregar el segundo (manifiestos).

```
Tu tarea es solo esta. No toques otros servicios ni otras etapas del worker.

Lee docs/prism/milton-prism-analysis-engine-spec.md (secciones 3 y 4).

El AnalysisSummary tiene un arreglo `technologies` donde escriben varias etapas:
la etapa de inventario (ya implementada) escribe los LENGUAJES detectados con
category = "language"; la etapa de manifiestos (próxima) escribirá frameworks y
librerías con su versión. Necesito que esos escritores no se pisen entre sí.

1. Define un helper de merge en la capa de aplicación del worker (no en infraestructura,
   no en el dominio) que toma el `technologies` existente y una nueva lista, y hace
   merge por clave (name + category) en vez de overwrite: agrega lo nuevo, actualiza
   campos vacíos de lo existente, nunca borra lo que escribió otra etapa.
2. Refactoriza la etapa de inventario (EnryLanguageDetector) para escribir vía ese helper.
3. Tests: que escribir inventario y luego una lista simulada de dependencias produce
   un `technologies` con ambos, sin duplicados y sin pérdida.

Respeta la regla de dependencias del Canon. Corre go build ./... y go test del worker.
```

---

## Tarea 2 — Parsers de manifiestos (un ecosistema por vez)

Pega este bloque **una vez por ecosistema**, sustituyendo `<ECOSISTEMA>` y `<MANIFIESTOS>`. Empieza por el del stack que vas a migrar primero.

Ecosistemas y sus manifiestos (Composer cubre Laravel **y** Symfony):

- **Maven** → `pom.xml`, `build.gradle` / `build.gradle.kts`
- **npm** → `package.json`, `package-lock.json` / `pnpm-lock.yaml` / `yarn.lock`
- **Composer** → `composer.json`, `composer.lock`
- **PyPI** → `requirements.txt`, `pyproject.toml`, `poetry.lock`
- **NuGet** → `*.csproj`, `packages.config`
- **RubyGems** → `Gemfile`, `Gemfile.lock`

```
Tu tarea es solo esta. No toques otros servicios, otras etapas, ni otros ecosistemas.

Lee docs/prism/milton-prism-analysis-engine-spec.md (secciones 3, 4 y 5).

Implementa el adaptador ManifestParser para el ecosistema <ECOSISTEMA>.

- Parsea los manifiestos de ese ecosistema (<MANIFIESTOS>). Prefiere el lockfile cuando
  exista, porque trae versiones resueltas; si no hay lockfile, usa el manifiesto declarativo.
- Produce []Dependency con: nombre del paquete, versión declarada, y ecosistema.
- Escribe el resultado en el arreglo `technologies` del AnalysisSummary usando el helper de
  merge de la Tarea 1, con category correcta (framework / library). NO pises lo que escribió
  el inventario ni otros ecosistemas.
- El parser vive como adaptador en infraestructura, detrás del puerto ManifestParser.
  El dominio y la aplicación no parsean archivos directamente.
- Agrega un fixture pequeño de este ecosistema bajo testdata/ y tests que verifiquen el
  parseo (paquetes, versiones, preferencia de lockfile).

Corre la verificación. No avances a otro ecosistema en esta tarea.
```

Repite hasta cubrir los seis. Con Composer tapás dos stacks (Laravel y Symfony) de una.

---

## Tarea 3 — Vigencia de versiones (después de manifiestos)

```
Tu tarea es solo esta. No toques otros servicios ni otras etapas.

Lee docs/prism/milton-prism-analysis-engine-spec.md (secciones 3, 5 y 8).

Implementa el adaptador VersionResolver.

- Dada (ecosistema, paquete), consulta el registry correspondiente para la última versión:
  Maven Central, npm, PyPI, Packagist, NuGet, RubyGems.
- Mapea el resultado a TechnologyStatus: Current / Outdated / EndOfLife, comparando la versión
  detectada contra la última, más datos conocidos de end-of-life.
- Escribe latest_version y status en las entradas de `technologies` correspondientes (vía el
  helper de merge, sin pisar otros campos).
- CACHEA por (ecosistema, paquete). Es la etapa más lenta y network-bound.
- DEGRADACIÓN: si el registry no responde, marca status como desconocido y sigue; nunca falles
  el análisis entero por un registry caído.
- El cliente HTTP vive como adaptador detrás del puerto VersionResolver. El dominio y la
  aplicación NO importan http directamente.
- Tests: con un cliente HTTP MOCKEADO. Nunca golpees los registries reales en los tests
  (serían lentos, flaky y dependientes de internet). Cubre: versión actual, versión vieja,
  y registry no disponible (degradación).

Corre la verificación.
```

---

## Tarea 4 — Vulnerabilidades con OSV.dev (última de Tier 1)

```
Tu tarea es solo esta. No toques otros servicios ni otras etapas.

Lee docs/prism/milton-prism-analysis-engine-spec.md (secciones 3, 5 y 8).

Implementa el adaptador VulnerabilityScanner con OSV.dev.

- Toma la lista de dependencias (ecosistema, paquete, versión) que ya produjeron los manifiestos.
- Consulta OSV.dev (query batch) y produce []Vulnerability con: identificador CVE, severidad
  (Low/Medium/High/Critical), componente afectado, descripción, y fixed_in_version.
- Escribe el resultado en el campo vulnerabilities del AnalysisSummary.
- CACHEA por (ecosistema, paquete, versión).
- DEGRADACIÓN: si OSV no responde, marca el escaneo como "unavailable" y sigue; nunca falles
  el análisis entero.
- El cliente OSV vive como adaptador detrás del puerto VulnerabilityScanner. El dominio y la
  aplicación NO importan el cliente HTTP ni el SDK de OSV directamente.
- Tests: con la respuesta de OSV MOCKEADA. Nunca golpees OSV real en los tests. Cubre:
  dependencia con vulnerabilidad conocida, dependencia sin vulnerabilidades, y OSV no disponible.

Corre la verificación.
```

---

## Hito de cierre

Cuando las cuatro tareas estén verdes (con los seis ecosistemas de la Tarea 2 cubiertos), el Tier 1 está completo en los siete stacks: el `AnalysisSummary` se llena con lenguajes, dependencias, versiones, status y vulnerabilidades reales de punta a punta, y la pantalla de análisis del frontend tiene datos de verdad. Ese es el momento de decidir entre el primer `LanguageAnalyzer` (Tier 2, grafo de acoplamiento) o el motor de descomposición.

### Reglas que aplican a todas las tareas

- Una etapa/ecosistema por tarea. No mezclar.
- Tests de etapas network-bound siempre con cliente mockeado, nunca contra servicios reales.
- Clientes HTTP/SDK externos siempre detrás de su puerto, como adaptador en infraestructura — nunca en dominio ni aplicación (regla de dependencias del Canon).
- Correr el bloque de verificación después de cada tarea antes de seguir.
```
