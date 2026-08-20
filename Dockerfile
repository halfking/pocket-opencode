FROM golang:1.26-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./backend/
RUN cd backend && go mod download
COPY backend ./backend
RUN cd backend && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/pocketd ./cmd/pocketd

FROM alpine:latest
RUN addgroup -S pocket && adduser -S -G pocket pocket && apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /out/pocketd /app/pocketd
RUN mkdir -p /app/data && chown -R pocket:pocket /app
USER pocket
EXPOSE 8088
HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=12 CMD wget -q -O- http://127.0.0.1:8088/healthz >/dev/null || exit 1
ENTRYPOINT ["/app/pocketd"]
