# Milton Prism — Ticket: agujero de resolución de aristas del extractor PHP

**Tipo:** mejora del motor de análisis (Tier-2, PHP/Laravel).
**Estado:** CERRADO (2026-06-20) — Tiers A, B y C implementados y validados; BookStack `unreachable_modules` 32 → 0.
**Abierto:** 2026-06-20, como consecuencia directa de B₂ (política live-set + `unreachable_modules`).
**Referencia obligatoria:** Canon + `milton-prism-go-profile.md`. Trabajo acotado al worker de análisis (etapa `ANALYZING`, stage 6/6b).

---

## 1. Contexto y origen

La política B₂ definió el "sistema vivo" como `fanIn>0 OR fanOut>0` (nodo con al menos una
arista en el grafo de dependencias) y agregó el reporte `unreachable_modules` (módulos con
código pero sin aristas estáticamente resolubles — *para revisar, nunca borrar*).

Al validar B₂ sobre BookStack, **32 módulos** quedaron como "no alcanzables estáticamente".
La inspección mostró que **no son código muerto ni magia de framework**: son **clases PHP
normales, claramente usadas**, que el extractor de imports PHP **no logra enlazar** al grafo.
El allowlist de entrypoints de framework (Kernels, Middleware, Providers, Commands, Factories,
Seeders, interfaces) ya filtró otras 27 — esas 32 son el residuo real.

Esto es la causa raíz del descuadre histórico **477 ≠ 339** en BookStack
(`module_count_production=477` vs `domain+infra=339`): el extractor PHP no resuelve suficientes
aristas, así que muchas clases reales aparecen como islas o sin clasificar.

---

## 2. Evidencia — las 32 islas de revisión de BookStack

Decodificadas del `AnalysisSummary` real (`unreachable_modules`) tras correr B₂:

| Patrón de uso no resuelto | n | Ejemplos |
|---|---|---|
| Clases value/servicio referenciadas por `new`/inyección | 10 | `Access\Oidc\OidcAccessToken`, `OidcJwtSigningKey`, `OidcOAuthProvider`, `OidcProviderSettings`, `OidcUserDetails`, `OidcUserinfoResponse`, `OidcJwtWithClaims`, `OidcInvalidKeyException`, `OidcIssuerDiscoveryException`, `ProvidesClaims` (interface) |
| Extensiones/conversores registrados por config (CommonMark) | 8 | `Entities\Tools\Markdown\{CheckboxConverter, CustomDivConverter, CustomImageConverter, CustomListItemRenderer, CustomParagraphConverter, CustomStrikeThroughExtension, CustomStrikethroughRenderer, SpacedTagFallbackConverter}` |
| Helpers de dominio | 3 | `Entities\Models\{EntityPageData, EntityQueryBuilder, EntityScope}` |
| Value objects | 2 | `Sorting\{BookSortMap, BookSortMapItem}` |
| Reglas de validación (`implements Rule`) | 2 | `Exports\ZipExports\{ZipFileReferenceRule, ZipUniqueIdRule}` |
| Enums | 2 | `Permissions\PermissionStatus`, `Activity\Models\WebhookTrackedEvent` |
| Otros (trait, builder, factory helper, excepción, tokenizer) | 5 | `Access\Controllers\ThrottlesLogins` (trait), `App\PwaManifestBuilder`, `Http\DownloadResponseFactory`, `Exceptions\PrettyException`, `Search\SearchTextTokenizer` |

(Total 32. `ProvidesClaims` es interface pero el allowlist la erró por nombre — degradó a
"revisar", que es el comportamiento seguro esperado.)

---

## 3. Causa raíz — patrones de PHP que el extractor no resuelve

`core/worker/analysis/infrastructure/adapters/php_import_extractor.go` y
`php_module_resolver.go` resuelven `use Namespace\Class;` a nivel de archivo, pero **no** las
referencias por nombre corto resueltas contra el **mismo namespace** ni contra los alias `use`.
Ése es el corazón del agujero: p.ej. `OidcService` y `OidcAccessToken` están ambos en
`BookStack\Access\Oidc`, así que la referencia es por nombre corto **sin `use`** (mismo
namespace) y el extractor no ve nada. Los constructos no capturados:

1. **type-hints** (parámetros, tipos de retorno, propiedades tipadas) por nombre corto.
2. **`new ClassName(...)`** (incluye `throw new`).
3. **referencias estáticas** `ClassName::método()`, `ClassName::CONST`, **`ClassName::class`**.
4. **`extends` / `implements`** (no genera arista hacia la base/interface).
5. **`use Trait;`** dentro del cuerpo de la clase (distinto del `use` de namespace a nivel archivo).
6. **enums** — type-hint de enum y `Enum::CASE` (lexicalmente igual a `::`, ver (3)).

En todos los casos hay que resolver el nombre corto a FQN vía (a) los `use` de alias del archivo
y (b) el **mismo namespace** del archivo. Corrección importante respecto del borrador previo: los
conversores/extensiones markdown **no** se registran por arrays de config — son `new X()` dentro
de `HtmlToMarkdown`/`MarkdownToHtml` (caen en (2)). **No se necesita** un mecanismo de "registro
por config" ni mover markdown al allowlist.

---

## 4. Conteo por mecanismo (ROI medido sobre las 32, con verdad de terreno)

Cloné `BookStackApp/BookStack@79a2e017` (el commit analizado) y localicé, para cada una de las 32,
la referencia real que crearía su arista. **El dato invierte el orden del borrador** (`implements`
NO es lo de mayor impacto):

| Tier / mecanismo | resuelve | % | evidencia (archivo:línea) |
|---|---|---|---|
| **A — type-hints + `new`/`throw new` + estáticas (`::método`/`::CONST`/`::class`)** | **29 / 32** | **91%** | type-hint: `OidcService.php:176` (`OidcAccessToken $x`); `new`: `OidcService.php:260` (`new OidcUserinfoResponse(`), `HtmlToMarkdown.php:88` (`new CheckboxConverter()`); `throw new`: `OidcJwtSigningKey.php:29`; retorno: `Controller.php:123` (`: DownloadResponseFactory`); `::class`: `Webhook.php:39` (`WebhookTrackedEvent::class`), `Entity.php:89` (`EntityQueryBuilder::class`); `::método`: `BookSortController.php:61` (`BookSortMap::fromJson`); enum `::CASE`: `EntityPermissionEvaluator.php:29` (`PermissionStatus::IMPLICIT_ALLOW`) |
| **B — `extends` / `implements`** | **2 / 32** | 6% | `OidcIdToken.php:5` (`extends OidcJwtWithClaims`), `ImageUploadException.php:5` (`extends PrettyException`) |
| **C — `use Trait;` en cuerpo de clase** | **1 / 32** | 3% | `LoginController.php:17` (`use ThrottlesLogins;`) |
| ~~D — registro por config / arrays~~ | **0** | 0% | (markdown son `new`, caen en A) |
| ~~E — solo docblock (`@throws`/`@var`)~~ | **0** | 0% | todas tienen referencia de código real (los `@throws` de OIDC tienen su `throw new` correspondiente) |

**Orden por ROI (corregido):**

1. **Tier A** — type-hints + `new` + estáticas (incl. `::class` y enum `::CASE`), resueltas contra
   mismo-namespace + alias `use`. Es el 91% de un solo golpe. **Empezar acá.**
2. **Tier B** — `extends`/`implements` (2 módulos: `OidcJwtWithClaims`, `PrettyException`).
3. **Tier C** — `use Trait;` en cuerpo (1 módulo: `ThrottlesLogins`).

`ProvidesClaims` (interface) se resuelve por type-hint de parámetro (`OidcUserDetails.php:35`,
Tier A) **además** de `implements` (Tier B) — confirma que Tier A sola ya la saca de la lista.

**Restricción de no-regresión:** Python y Conduit/notiplan no deben cambiar (su grafo de imports ya
es completo). El extractor PHP es independiente (`isPHPEdge`/separador backslash); el genérico no se
toca.

---

## 5. Criterio de gate — delta ESTRUCTURAL, no delta de score

El gate mide **estructura**, no el agregado de score. (Lección de Tier A: "el score debe moverse"
es un proxy defectuoso — el score es band-cuantizado y un cambio estructural real e importante
puede no cruzar ningún umbral de banda, dejando el agregado igual. Ver más abajo.)

**Criterio de éxito (sobre `BookStackApp/BookStack@79a2e017`):**
1. **`unreachable_modules` baja**, y **cada módulo que sale de la lista tiene su `archivo:línea`**
   y el constructo que crea la arista (sección 4 + sección 6). Una baja sin respaldo no se acepta.
2. **`domain+infra`, fan-ins y nodos-vivos se mueven de forma explicable** por las aristas
   resueltas (más clases con aristas → más clasificadas, más fan-in en los destinos).
3. **El oráculo de set exacto pasa** (sección 6): ni una arista de más (sobre-resolución) ni de
   menos (sub-resolución).
4. **Control Python delta 0**: Conduit (21) y notiplan (13) **idénticos** — su grafo ya era
   completo y el extractor PHP no debe agregarles ni una arista. Un edge nuevo en Python es un
   falso positivo de resolución y bloquea el gate.

**Lo que NO es criterio de éxito ni de fracaso por sí solo: el score.**
El score puede moverse o no. Es band-cuantizado: cambios **intra-banda** (p.ej. domain ratio
54%→55% con ambos ≥30%; el hub top subiendo fan-in sin cruzar a la siguiente banda de severidad;
mismo número de god-modules y de clusters) **no mueven el agregado** aunque la estructura mejore
de verdad. Que el score quede igual NO significa que las aristas no se resolvieron — eso lo prueban
(1)–(4). Inversamente, si el score se mueve, el delta debe ser explicable por estructura real
(hubs/ciclos/clusters/clasificación que ahora sí se detectan), nunca un número que apareció.

**`module_count_production` ~477 estable** (desacoplado del live-set; el fix cambia cuántas clases
tienen aristas, no el inventario — que nadie lo "arregle" a la baja).

### 5.1 Resultado real (Tiers A+B+C aplicados, re-corrida de los 3 oráculos)

| Métrica | Conduit (10) | notiplan (11) | BookStack (12) |
|---|---|---|---|
| `unreachable_modules` | 0 | 0 | **32 → 0** (A: 32→2, B/C: 2→0) |
| `module_count_production` | 21 | 13 | **477** (estable) |
| `domain+infra` | 21 | 13 | **339 → 378** |
| nodos vivos (B₂) | 28 | 15 | **547 → 631** |
| score (band-cuantizado) | 83 | 0 | 85 → 85 (intra-banda) |

- **Control Python (Conduit/notiplan): delta 0 en todo.** Confirma cero sobre-resolución PHP.
- **Score BookStack 85→85**: estructura mejoró (hub `Entity` fan-in 65→69; `domain+infra`
  339→378; +84 nodos vivos) pero los cambios quedaron **intra-banda** (ratio ≥30%, hub en banda
  "minor", 2 god-modules, 17 clusters) → el agregado no se movió. Consistente, no contradictorio.

### 5.2 El descuadre histórico 477≠339 quedó EXPLICADO (cerrado, no escondido)

Antes: `module_count_production=477` vs `domain+infra=339` (gap 138) parecía inflación. Resuelto:

- `domain+infra` subió **339 → 378** al resolver las islas (clases que ahora tienen aristas entran
  a clasificación).
- El resto del gap es la **capa application (≈93 módulos)**, que es una categoría legítima y
  distinta (`module_classification.application_modules`), **no inflación**.
- `domain(260) + infra(118) + application(93) = 471`; el remanente hasta 477 son ~6 entry points de
  framework que quedan como islas suprimidas (reachable-por-framework, contados como producción).

477 = domain + infra + application + (islas framework suprimidas). Cada módulo está contabilizado
en una categoría legítima. El misterio histórico está cerrado.

---

## 6. Oráculo de verdad de terreno (criterio de aceptación duro)

El riesgo opuesto al de hoy: un extractor **demasiado agresivo** que resuelva de más bajaría
`unreachable_modules` metiendo **aristas falsas** que ensucian el grafo (y moverían el score por
razones inventadas). Para distinguir "resolví aristas reales" de "inventé aristas":

> **Cada reducción de `unreachable_modules` (y cada arista nueva que mueva el score) debe
> justificarse con la ubicación `archivo:línea` en el fuente PHP de BookStack donde ocurre la
> referencia que crea esa arista.**

- "Bajé unreachable de 32 a N" **no se acepta** sin, por cada módulo que salió de la lista, el
  `archivo:línea` y el constructo (type-hint / `new` / `::` / `extends` / `implements` / trait) que
  lo respalda. La tabla de la sección 4 es el formato y el set de referencia inicial (las 32 ya
  tienen su `archivo:línea`).
- Inversamente: **ninguna arista nueva sin un `archivo:línea` que la respalde.** Una arista que el
  extractor produzca y que no corresponda a una referencia real en el fuente es un bug de
  sobre-resolución y bloquea el gate, aunque baje `unreachable`.
- Recomendado: un test de integración que, sobre un fixture PHP reducido con casos de cada tier,
  afirme exactamente el set de aristas esperado (ni de más ni de menos).

---

## 7. Gate block (correr después de cada cambio, CGO_ENABLED=1)

```
buf lint
go build ./...
go vet ./...
go test ./core/worker/analysis/... ./core/services/analysis/...
```

Archivos previsibles: `php_import_extractor.go` (+ test), `php_module_resolver.go` (+ test).
(`framework_allowlist.go` NO se toca — markdown son `new`, no config.) Mongo requerido para
integración.

---

## 8. Referencias

- B₂ y `unreachable_modules`: `core/worker/analysis/application/pipeline.go` (stage 6),
  `framework_allowlist.go`, proto `UnreachableModule` en `types/analysis/v1/analysis.proto`.
- Clasificación PHP: `php_module_classifier.go`, `language_aware_classifier.go`.
- Verdad de terreno usada en la sección 4: `BookStackApp/BookStack@79a2e017` (commit analizado).
- Memoria del proyecto: trabajo B₂ y el detalle de las 32.
