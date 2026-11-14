# syntax=docker/dockerfile:1

# One shared source stage. Every service build reuses it, so the module graph
# is downloaded once instead of once per command.
FROM golang:1.27.4-bookworm AS src

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The scraper writes its cursor file here. Creating the directory in the build
# stage lets a fresh named volume inherit nonroot ownership.
RUN mkdir -p /out/data

FROM src AS build

ARG CMD
ARG VERSION=dev

RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X fleetpulse/internal/version.Version=${VERSION}" \
    -o /out/app ./cmd/${CMD}

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/app /app
COPY --from=build --chown=nonroot:nonroot /out/data /data

ENTRYPOINT ["/app"]
