ARG BUILD_FROM=alpine:3.20
FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/openwrt-monitor .

FROM ${BUILD_FROM}
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /bin/openwrt-monitor /bin/openwrt-monitor
ENTRYPOINT ["/bin/openwrt-monitor"]
