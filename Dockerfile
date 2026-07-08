FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /diskwake .

FROM alpine:3.20

RUN apk add --no-cache tzdata

COPY --from=build /diskwake /usr/local/bin/diskwake

ENTRYPOINT ["/usr/local/bin/diskwake"]
CMD ["-config", "/etc/diskwake/config.yaml"]
