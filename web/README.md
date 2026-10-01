# lanepool web dashboard

Admin UI for lanepool v2: Vite + React 19 + TypeScript, Tailwind CSS v4, shadcn/ui,
TanStack Router / Query / Table and Recharts. The API contract is `../docs/api.md`;
its types are mirrored in `src/lib/types.ts`.

## Development

```sh
cd web
npm install
npm run dev        # http://localhost:5173
```

The dev server proxies `/api`, `/healthz`, `/readyz` and `/metrics` to
`http://localhost:8000` (override with `LANEPOOL_API=http://host:port npm run dev`),
so the session cookie is same-origin and works without CORS.

## Build

```sh
npm run build      # tsc -b, then vite build into web/dist
npm run lint       # oxlint, warnings are errors
npm run typecheck  # tsc -b only
```

The Go server embeds `web/dist` (`//go:embed all:dist`) and serves it with an SPA
fallback to `index.html`. `dist/.gitkeep` is committed and recreated after every build
(Vite plugin plus `postbuild`), so the embed works in a fresh checkout before the
first build.

## Layout

- `src/lib/types.ts`: API types, exactly as in `docs/api.md`
- `src/lib/api.ts`: fetch wrapper (`ApiError`, 401 redirect) and typed endpoints
- `src/lib/format.ts`: bytes, durations, relative times, flags
- `src/hooks/`: TanStack Query hooks per resource, SSE stream (`use-stream.ts`)
- `src/components/`: shared UI (data table, multi-select, tag input, charts…); `ui/` is shadcn
- `src/pages/`: one module per route; `src/router.tsx` holds the route tree
