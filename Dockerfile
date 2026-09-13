FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /spool ./cmd/spool

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /spool /spool
ENV SPOOL_COOKIE_SECURE=true
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/spool"]
