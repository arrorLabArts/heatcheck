FROM golang:1.25.12-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/heatcheck-api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/heatcheck-worker ./cmd/worker
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/heatcheck-admin ./cmd/admin

FROM alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates \
    && addgroup -S heatcheck \
    && adduser -S -G heatcheck heatcheck
USER heatcheck

FROM runtime AS app

USER root
RUN apk add --no-cache ffmpeg
USER heatcheck
COPY --from=build /out/heatcheck-api /usr/local/bin/heatcheck-api
COPY --from=build /out/heatcheck-worker /usr/local/bin/heatcheck-worker
COPY --from=build /out/heatcheck-admin /usr/local/bin/heatcheck-admin
EXPOSE 8080
ENTRYPOINT ["heatcheck-api"]
