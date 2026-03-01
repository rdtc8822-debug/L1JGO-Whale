# ============================================================
#  L1JGO-Whale 遊戲伺服器 — Docker 多階段建構
# ============================================================

# Stage 1: 編譯 Go 二進位
FROM golang:1.23-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o l1jgo ./cmd/l1jgo

# Stage 2: 最小化執行環境
FROM alpine:latest
WORKDIR /app

# 複製編譯產物
COPY --from=builder /build/l1jgo .

# 複製運行時靜態資料
COPY config/ ./config/
COPY data/yaml/ ./data/yaml/
COPY map/ ./map/
COPY scripts/ ./scripts/

# 建立血盟紋章目錄
RUN mkdir -p emblem

EXPOSE 7001
CMD ["./l1jgo"]
