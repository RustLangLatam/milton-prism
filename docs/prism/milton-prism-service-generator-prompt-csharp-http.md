# Milton Prism — Hexagonal Service Generator (C# / ASP.NET Core HTTP Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, gate-passing hexagonal **C# + .NET + ASP.NET Core Minimal API + HTTP** microservice that obeys the Architecture Canon and the C# Language Profile.

This is the **HTTP-native** sibling of `milton-prism-service-generator-prompt-csharp.md` (gRPC). Read that prompt AND `milton-prism-csharp-profile.md` (especially **A.4-HTTP**) and `milton-prism-architecture-canon.md` in full first. Everything in the gRPC prompt applies EXCEPT the transport: §§0–4, 6, 7 carry over unchanged; this document overrides the transport-specific steps below.

---

## Transport override (CRITICAL — no gRPC server)

The **ASP.NET Core Minimal API app is the ONLY runtime entrypoint.** Per C# Profile A.4-HTTP:

- **There MUST be no `AddGrpc()`, no `MapGrpcService<>`, no `*Grpc.cs` service base wired, and no `Infrastructure/Grpc/` package.** Do NOT bootstrap a gRPC server. The `.csproj` `<Protobuf>` items use `GrpcServices="None"` (message classes only — no service base generated).
- You MUST still write the authoritative `.proto` to the canonical `protobuf/proto/milton_prism/services/<service>/v1/...` path WITH a `google.api.http` annotation on EVERY RPC — the platform derives `docs/openapi.yaml` from those annotations. Without them the OpenAPI is empty.
- Model request/response messages as the proto-generated message classes (or DTOs mapping to them), serialized as JSON. You do NOT need the `*Grpc.cs` service base at runtime.
- Implement **endpoint mappers in `Infrastructure/Http/`** (`app.MapGet`/`MapPost`/`MapPut`/`MapDelete`), **1:1 with the proto RPCs**, mounted on the verb + path declared by each RPC's `google.api.http` annotation. Map `DomainError` → HTTP status via an exception-handling middleware / `Results.Problem` (`1xx`→400, not-found→404, forbidden→403, conflict→409, `5xx`→500), returning `{ code, message }`.

## Build-order override

Replace the gRPC steps 5–7 of the base prompt with:

- **5. Handlers** → `Infrastructure/Http/`: Minimal API endpoint mappers 1:1 with the RPCs, honoring the `google.api.http` routes; delegate to the use case; map errors via the shared middleware. Extract the authenticated identity via the ASP.NET Core authentication middleware (C# Profile A.12), never inline token parsing.
- **6. config / wiring** → the composition root registers ports → adapters → use case → endpoint mappers on `IServiceCollection`, plus the auth middleware.
- **7. Program.cs** → `var builder = WebApplication.CreateBuilder(args); /* DI + AddAuthentication().AddJwtBearer(...) */ var app = builder.Build(); app.UseAuthentication(); app.UseAuthorization(); /* MapXxx endpoints */ app.Run();` bound to `GRPC_HOST`/`GRPC_PORT` (here an HTTP port). NO `AddGrpc()`/`MapGrpcService`.

## Auth override

JWT (C# Profile A.12, HTTP variant): wire `AddAuthentication().AddJwtBearer(...)` + `UseAuthentication()`/`UseAuthorization()` covering the protected endpoints; expose the authenticated `sub` to handlers via `HttpContext.User`. `Microsoft.AspNetCore.Authentication.JwtBearer`; secret/issuer/audience from `IConfiguration`/`ENV`; reject `alg=none`; failures → HTTP 401.

## Gate (unchanged shape, HTTP entrypoint)

```bash
buf lint                                          # from protobuf/ — proto + google.api.http must pass
dotnet build                                      # from csharp/ — restore from the warmed NuGet cache + compile
dotnet test                                       # MUST pass — THE GATE (endpoint/integration tests welcome)
```

## HTTP-specific self-audit additions

- Zero `AddGrpc()` / `MapGrpcService` / `Infrastructure/Grpc/` in the deliverable? → must be yes
- The `.csproj` `<Protobuf>` items use `GrpcServices="None"` (no service base)? → must be yes
- Every RPC carries a `google.api.http` annotation in the proto? → must be yes
- Endpoint mappers are 1:1 with the RPCs and honor the annotated verb+path? → must be yes
- The ASP.NET Core app is the only entrypoint (no gRPC bootstrap)? → must be yes

All other rules, the report format (§6), and the rejection triggers (§7) of the base gRPC prompt apply unchanged — except gRPC bootstrap is REQUIRED to be ABSENT here.
