# 多阶段构建：产物为静态单二进制（前端可后续 embed 进来）。
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.20
RUN adduser -D -u 10001 custos
USER custos
COPY --from=build /out/server /usr/local/bin/server
ENV CUSTOS_HTTP_ADDR=:8080 CUSTOS_DATABASE_DSN=/data/custos.db
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["server"]
