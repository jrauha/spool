FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /spool ./cmd/spool
RUN mkdir -p /var/lib/spool/assets && chown -R 65532:65532 /var/lib/spool

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /spool /spool
COPY --from=build --chown=nonroot:nonroot /var/lib/spool /var/lib/spool
ENV SPOOL_COOKIE_SECURE=true
ENV SPOOL_HOME=/var/lib/spool
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/spool"]
