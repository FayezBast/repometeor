# syntax=docker/dockerfile:1.7
FROM node:26.13.0-alpine AS deps
RUN corepack enable
WORKDIR /app
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/web/package.json apps/web/package.json
RUN pnpm install --frozen-lockfile --filter @repometeor/web...

FROM node:26.13.0-alpine AS build
RUN corepack enable
WORKDIR /app
ARG NEXT_PUBLIC_API_URL
ARG NEXT_PUBLIC_DEMO_MODE
ARG NEXT_PUBLIC_GITHUB_REPO_URL
ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL \
    NEXT_PUBLIC_DEMO_MODE=$NEXT_PUBLIC_DEMO_MODE \
    NEXT_PUBLIC_GITHUB_REPO_URL=$NEXT_PUBLIC_GITHUB_REPO_URL
COPY --from=deps /app/node_modules ./node_modules
COPY --from=deps /app/apps/web/node_modules ./apps/web/node_modules
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/web ./apps/web
RUN pnpm --filter @repometeor/web build

FROM node:26.13.0-alpine AS runtime
WORKDIR /app
ENV NODE_ENV=production PORT=3000 HOSTNAME=0.0.0.0
RUN addgroup -S repometeor && adduser -S -G repometeor -u 10001 repometeor
COPY --from=build --chown=repometeor:repometeor /app/apps/web/.next/standalone ./
COPY --from=build --chown=repometeor:repometeor /app/apps/web/.next/static ./apps/web/.next/static
COPY --from=build --chown=repometeor:repometeor /app/apps/web/public ./apps/web/public
USER repometeor
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=3s --retries=3 CMD wget --quiet --spider http://127.0.0.1:3000 || exit 1
CMD ["node", "apps/web/server.js"]
