# ---------- 构建阶段 ----------
FROM golang:1.24-alpine AS builder

WORKDIR /src

# 先拷贝依赖描述文件以利用缓存
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 静态编译，去掉调试信息
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

# ---------- 运行阶段 ----------
FROM alpine:3.20

# ca-certificates 用于访问 HTTPS 上游；tzdata 用于正确的时间计算
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /out/server /app/server
COPY --from=builder /out/worker /app/worker
COPY --from=builder /out/migrate /app/migrate

# 上传目录归属非 root 用户
RUN mkdir -p /app/storage/resumes && chown -R appuser:appuser /app

# 以非 root 身份运行
USER appuser

ENV TZ=Asia/Shanghai \
    APP_ENV=production \
    BIND_ALL=true

EXPOSE 9090

CMD ["/app/server"]
