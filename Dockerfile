FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.20

RUN adduser -D -u 10001 keel
COPY --from=build /out/api /api
USER keel
EXPOSE 8080
ENTRYPOINT ["/api"]
