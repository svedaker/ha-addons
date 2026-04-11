FROM golang:alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /bin/climax2mqtt

FROM alpine
ENV GOMEMLIMIT=32MiB
ENV GOGC=50
COPY --from=build /bin/climax2mqtt /bin/climax2mqtt
ENTRYPOINT ["/bin/climax2mqtt"]
