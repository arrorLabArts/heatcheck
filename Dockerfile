FROM golang:1.25.3-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/heatcheck-api ./cmd/api

FROM alpine:3.22

RUN addgroup -S heatcheck && adduser -S -G heatcheck heatcheck
COPY --from=build /out/heatcheck-api /usr/local/bin/heatcheck-api
USER heatcheck
EXPOSE 8080
ENTRYPOINT ["heatcheck-api"]
