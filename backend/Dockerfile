FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/cosmo-api ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S cosmo && adduser -S cosmo -G cosmo
USER cosmo
COPY --from=build /out/cosmo-api /usr/local/bin/cosmo-api
EXPOSE 8080
ENTRYPOINT ["cosmo-api"]
