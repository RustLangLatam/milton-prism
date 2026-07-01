# Milton Prism — Hexagonal Service Generator (Ruby / Rails API|Sinatra HTTP Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, gate-passing hexagonal **Ruby + Bundler + Rails API (or Sinatra) + HTTP** microservice that obeys the Architecture Canon and the Ruby Language Profile.

This is the **HTTP-native** sibling of `milton-prism-service-generator-prompt-ruby.md` (gRPC). Read that prompt AND `milton-prism-ruby-profile.md` (especially **A.4-HTTP**) and `milton-prism-architecture-canon.md` in full first. Everything in the gRPC prompt applies EXCEPT the transport: §§0–4, 6, 7 carry over unchanged; this document overrides the transport-specific steps below.

---

## Transport override (CRITICAL — no gRPC server)

The **Rails API (API-only) or Sinatra app is the ONLY runtime entrypoint.** Per Ruby Profile A.4-HTTP:

- **There MUST be no `GRPC::RpcServer`, no `GRPC::GenericService`, no `*_services_pb.rb` stub wired, and no `infrastructure/grpc/` package.** Do NOT bootstrap a gRPC server.
- You MUST still write the authoritative `.proto` to the canonical `protobuf/proto/milton_prism/services/<service>/v1/...` path WITH a `google.api.http` annotation on EVERY RPC — the platform derives `docs/openapi.yaml` from those annotations. Without them the OpenAPI is empty.
- Model request/response messages as **POROs** (the generated `*_pb.rb` message classes may back them), serialized as JSON. You do NOT need the `*_services_pb.rb` grpc stub at runtime.
- Implement **controllers/routes in `infrastructure/http/`** (Rails `ActionController::API` controllers or Sinatra routes), **1:1 with the proto RPCs**, mounted on the verb + path declared by each RPC's `google.api.http` annotation. Map `DomainError` → HTTP status via a `rescue_from`/Rack mapper (`1xx`→400, not-found→404, forbidden→403, conflict→409, `5xx`→500), returning `{ code, message }`.

## Build-order override

Replace the gRPC steps 5–7 of the base prompt with:

- **5. Handlers** → `infrastructure/http/`: Rails `ActionController::API` controllers (or Sinatra routes) 1:1 with the RPCs, honoring the `google.api.http` routes; delegate to the use case; map errors via the shared mapper. Extract the authenticated identity via a Rack middleware / `before_action` (Ruby Profile A.12), never inline token parsing.
- **6. config / wiring** → the composition root constructs ports → adapters → use case → controllers, plus the auth middleware.
- **7. main.rb / config.ru** → boot the Rails/Sinatra Rack app under Puma (`config.ru` + `puma`), binding `GRPC_HOST`/`PORT` (here an HTTP port). NO gRPC server.

## Auth override

JWT (Ruby Profile A.12, HTTP variant): wire as a **Rack middleware** or a Rails `before_action` covering the protected routes; expose the authenticated `sub` to handlers. The `jwt` gem; secret/issuer/audience from `ENV`; reject `alg=none`; failures → HTTP 401.

## Gate (unchanged shape, HTTP entrypoint)

```bash
buf lint                                          # from protobuf/ — proto + google.api.http must pass
bundle install                                    # from ruby/ — resolve from the warmed GEM_HOME
ruby -c $(find services/<service> -name '*.rb')   # every file parses — exit 0
bundle exec rspec                                 # MUST pass — THE GATE (request specs welcome)
```

## HTTP-specific self-audit additions

- Zero `GRPC::RpcServer` / `GRPC::GenericService` / `infrastructure/grpc/` in the deliverable? → must be yes
- Every RPC carries a `google.api.http` annotation in the proto? → must be yes
- Controllers/routes are 1:1 with the RPCs and honor the annotated verb+path? → must be yes
- The Rails/Sinatra app is the only entrypoint (no gRPC bootstrap)? → must be yes

All other rules, the report format (§6), and the rejection triggers (§7) of the base gRPC prompt apply unchanged — except gRPC bootstrap is REQUIRED to be ABSENT here.
