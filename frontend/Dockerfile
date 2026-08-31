# ---------- 依赖阶段 ----------
FROM node:20-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --no-audit --no-fund

# ---------- 构建阶段 ----------
FROM node:20-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

# ---------- 运行阶段 ----------
FROM node:20-alpine AS runner
WORKDIR /app

ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000

RUN addgroup -g 10001 nodegrp && adduser -D -u 10001 -G nodegrp nodeusr

COPY --from=builder /app/public ./public
COPY --from=builder /app/.next ./.next
COPY --from=deps /app/node_modules ./node_modules
COPY --from=builder /app/package.json ./package.json

RUN chown -R nodeusr:nodegrp /app

USER nodeusr

EXPOSE 3000

CMD ["npx", "next", "start", "-p", "3000"]
