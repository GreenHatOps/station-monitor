FROM golang:1.22-alpine AS build
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /station-monitor ./cmd/station-monitor

FROM alpine:3.20
RUN apk add --no-cache smartmontools && rm -rf /var/cache/apk/*
COPY --from=build /station-monitor /station-monitor
EXPOSE 9101
ENTRYPOINT ["/station-monitor"]
