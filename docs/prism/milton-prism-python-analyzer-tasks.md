# Milton Prism — Tareas para Claude Code: Python LanguageAnalyzer (Tier 2, etapa 6)

Copia y pega **un bloque a la vez**, en orden. Verifica entre cada uno. Esto implementa **solo la etapa 6** (grafo de dependencias internas con acoplamiento) para Python. La etapa 7 (clustering semántico) **no va acá** — pertenece al motor de descomposición (artefacto siguiente), porque su salida es el `RestructurePlan`, no el `AnalysisSummary`.

**Entrada vs salida:** este analyzer LEE código Python/Flask (el monolito de entrada). No tiene nada que ver con el lenguaje de salida de los microservicios, que sigue siendo Go (Perfil Go). Analizás Python, generás Go; son independientes.

**Heads-up de build:** `go-tree-sitter` usa CGO (bindings de C). El worker pasa a requerir CGO habilitado y un toolchain de C; ajusta el Dockerfile del worker en consecuencia.

---

## Verificación (correr después de cada tarea)

```
buf lint
go build ./...
go vet ./...
go test ./core/worker/analysis/...
```

CGO debe estar habilitado para que el build del worker compile (`CGO_ENABLED=1`).

---

## Tarea 1 — Extracción de imports con tree-sitter

```
Tu tarea es solo esta. No toques otras etapas, otros servicios, ni los parsers de manifiestos.

Lee docs/prism/milton-prism-analysis-engine-spec.md (secciones 3, 4 y 5; el puerto LanguageAnalyzer y DependencyGraphBuilder).

Implementa SOLO la extracción de imports de Python, como primer pedazo del PythonLanguageAnalyzer:

- Agrega go-tree-sitter y la gramática tree-sitter-python como dependencias (CGO).
- Crea un adaptador en infraestructura que recorre los archivos .py del workspace y, con tree-sitter,
  extrae cada sentencia de import a una forma estructurada:
    RawImport { ImportingFile string; Module string; IsRelative bool; RelativeLevel int; Names []string }
  Cubre estas formas:
    import a.b.c
    import a.b.c as x
    from a.b import c, d
    from a.b import (c, d)
    from . import x            (relativo, nivel 1)
    from ..pkg import y        (relativo, nivel 2)
    from .mod import z
- NO resuelvas todavía a qué módulo apunta cada import; solo extrae la forma cruda. La resolución es la Tarea 2.
- Imports dinámicos (importlib, __import__) no se parsean estáticamente: documéntalo como limitación conocida.
- Señales de Flask (el monolito de entrada es Flask): además de los imports, registra cada definición
  Blueprint(name, ...) y cada app.register_blueprint(bp, url_prefix=...), capturando el nombre del blueprint,
  el archivo donde se define, y su url_prefix si lo tiene. Guárdalo como metadato aparte (NO es una arista del
  grafo); el perfil de framework lo consumirá en la descomposición. Si el proyecto no usa blueprints, el
  metadato queda vacío y no pasa nada.
- El adaptador vive detrás de su interfaz; nada de tree-sitter en dominio ni aplicación.
- Fixtures: archivos .py pequeños bajo testdata/ con cada forma de import. Tests que verifiquen la extracción
  cruda (módulo, relativo sí/no, nivel, nombres).

Corre la verificación con CGO_ENABLED=1.
```

---

## Tarea 2 — Resolución de módulos (interno vs externo)

```
Tu tarea es solo esta. No toques otras etapas ni la extracción de la Tarea 1 salvo para consumirla.

Lee la sección 5 del spec (resolución de imports por lenguaje).

Implementa la resolución de los RawImport de Python a módulos del repositorio:

- Construye el mapa del proyecto: para cada archivo .py del workspace, su nombre de módulo en notación
  punteada. Maneja:
    - layout plano (módulo en la raíz: foo.py -> "foo")
    - paquetes con __init__.py (a/b/__init__.py -> "a.b")
    - layout src/ (src/myapp/foo.py -> "myapp.foo"): detecta raíces de import (src/, o el directorio que
      contiene el paquete top-level) y mapéalas.
    - namespace packages PEP 420 (paquetes sin __init__.py).
- Resuelve cada RawImport:
    - Absoluto (import a.b.c / from a.b import c): si el nombre punteado resuelve a un archivo/paquete DENTRO
      del repo, es INTERNO y apunta al módulo más profundo que exista en el repo; si no resuelve dentro del
      repo, es EXTERNO (lo descarta para el grafo: las externas ya las cubrió Tier 1).
    - Relativo (from . / from ..): resuelve contra el paquete del archivo que importa, subiendo RelativeLevel
      niveles. Siempre interno.
- Produce ResolvedImport { FromModule string; ToModule string } solo para los internos.
- Fixtures: un mini-proyecto Python bajo testdata/ con estructura de paquetes, __init__.py, un layout src/,
  imports relativos y absolutos, y al menos un import externo (p.ej. import requests). Tests que verifiquen
  que cada import resuelve al módulo interno correcto o se marca externo correctamente.

Corre la verificación.
```

---

## Tarea 3 — Grafo, acoplamiento y cableado como etapa 6

```
Tu tarea es solo esta. No toques las Tareas 1 y 2 salvo para consumirlas.

Lee las secciones 3, 4 y 7 del spec.

1. Construye el grafo dirigido de módulos a partir de los ResolvedImport internos:
   - Nodos = módulos internos. Aristas = "módulo A importa módulo B".
   - Peso de la arista = cantidad de referencias de import de A hacia B (acoplamiento).
   - Produce []DependencyEdge (FromModule, ToModule, Weight) — el tipo del AnalysisSummary.
2. Completa el PythonLanguageAnalyzer implementando el puerto LanguageAnalyzer:
   Ecosystem() devuelve Python; ResolveImports(ctx, workspace) devuelve []DependencyEdge usando Tareas 1+2.
3. Cablea la etapa 6 en el pipeline detrás del puerto DependencyGraphBuilder:
   - Un registry de LanguageAnalyzer keyed por lenguaje/ecosistema.
   - La etapa 6 itera los lenguajes DETECTADOS por el inventario (etapa 2); para cada uno con analyzer
     registrado, corre ResolveImports y acumula las aristas en AnalysisSummary.dependency_graph.
   - HUECO: si un lenguaje detectado no tiene LanguageAnalyzer registrado, NO falles — salta ese lenguaje
     y deja constancia en el log/summary ("deep analysis not available for <lang>"). Mismo patrón que los
     huecos de perfil del generador.
4. Tests sobre el mini-proyecto de la Tarea 2:
   - el grafo tiene las aristas internas esperadas con sus pesos
   - los imports externos NO producen aristas
   - un lenguaje detectado sin analyzer registrado se saltea sin error (comportamiento de hueco)
   - idempotencia: correr la etapa dos veces no duplica aristas

Corre la verificación. Con esto la etapa 6 queda completa para Python.
```

---

## Cierre y qué sigue

Cuando las tres tareas estén verdes, el motor de análisis produce, para repos Python, el **grafo de dependencias internas con acoplamiento** en `AnalysisSummary.dependency_graph`. Ese es el insumo que faltaba para la descomposición.

La **etapa 7 (clustering semántico → bounded contexts)** se construye en el **motor de descomposición**, no acá: toma este grafo + el perfil de framework de Python/Django + hace el clustering (parte determinista por comunidades de acoplamiento, parte LLM para nombrar e interpretar) y produce el `RestructurePlan`. Ese es el próximo artefacto, y es donde entra el primer uso de modelo en el backend (detrás de un puerto de router, con adaptador simple ahora y router multimodelo después).

### Reglas que aplican a todas las tareas

- Una etapa por tarea. No mezclar.
- tree-sitter y cualquier parsing solo en adaptadores de infraestructura, nunca en dominio ni aplicación (regla de dependencias del Canon).
- El grafo solo contiene aristas INTERNAS (módulo a módulo dentro del repo); las dependencias externas son cosa de Tier 1.
- El comportamiento de hueco (lenguaje sin analyzer) es saltar + reportar, nunca fallar ni inventar.
- Correr la verificación con CGO_ENABLED=1 después de cada tarea.
```
