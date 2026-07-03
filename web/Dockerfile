# Production image for the MindMesh React SPA.
# Build context is web/ (self-contained — the frontend needs nothing outside it):
#
#   docker build -t mindmesh/web:dev web/
#
# Multi-stage: build the Vite bundle, then serve it as static files via nginx.

# ---- build stage ---------------------------------------------------------
FROM node:22-alpine AS build
WORKDIR /app

# Install against the lockfile first for layer caching; the npm cache is a
# BuildKit cache mount so repeated builds don't refetch every package.
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci

# Build the production bundle (tsc -b && vite build -> dist/).
COPY . ./
RUN npm run build

# ---- runtime stage -------------------------------------------------------
FROM nginx:1.27-alpine

# Full custom config: :8080, SPA fallback, /healthz, all writable state under
# /tmp so the container can run non-root with a read-only root filesystem.
COPY nginx.conf /etc/nginx/nginx.conf
COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 8080
# The base image's entrypoint runs nginx in the foreground.
