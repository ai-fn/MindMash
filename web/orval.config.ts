import { defineConfig } from 'orval'

// Generates a typed TanStack Query v5 client (hooks + query keys + fetchers) for each
// backend service from its committed grpc-gateway OpenAPI v2 swagger file. All four
// projects share the same custom fetch mutator (./src/lib/api/mutator.ts) so every
// generated call goes through the bearer-token injection + single-flight 401 refresh
// logic — see docs/detailed/12-web-spa.md and task-2-brief.md.
//
// NOTE: the installed orval version (8.18.0) names the output-formatting option
// `output.formatter: 'prettier'`, not `output.prettier: true` as in early drafts of the
// brief; using `formatter` here so `npm run format:check` stays green on generated code.
const services = ['identity', 'collection', 'scheduler', 'sync'] as const

export default defineConfig(
  Object.fromEntries(
    services.map((svc) => [
      svc,
      {
        input: {
          target: `../api/openapi/${svc}/v1/${svc}.swagger.json`,
        },
        output: {
          target: `./src/lib/api/${svc}/`,
          mode: 'tags-split',
          client: 'react-query',
          httpClient: 'fetch',
          formatter: 'prettier',
          override: {
            mutator: {
              path: './src/lib/api/mutator.ts',
              name: 'customFetch',
            },
          },
        },
      },
    ]),
  ),
)
