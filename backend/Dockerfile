FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/cosmo-api ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/cosmo-migrate ./cmd/migrate &&     CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/cosmo-seed ./cmd/seed &&     CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/cosmo-mcpdemo ./cmd/mcpdemo

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S cosmo && adduser -S cosmo -G cosmo
USER cosmo
COPY --from=build /out/cosmo-api /usr/local/bin/cosmo-api
COPY --from=build /out/cosmo-migrate /usr/local/bin/cosmo-migrate
COPY --from=build /out/cosmo-seed /usr/local/bin/cosmo-seed
COPY --from=build /out/cosmo-mcpdemo /usr/local/bin/cosmo-mcpdemo
EXPOSE 8080
ENTRYPOINT ["cosmo-api"]
