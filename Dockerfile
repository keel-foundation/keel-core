FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM alpine:3.20

RUN apk add --no-cache ffmpeg ca-certificates \
    && adduser -D -u 10001 keel
COPY --from=build /out/api /api
COPY --from=build /out/seed /seed
COPY --from=build /out/worker /worker
USER keel
EXPOSE 8080 8083
ENTRYPOINT ["/api"]
