# syntax=docker/dockerfile:1

FROM golang:1.27.4-bookworm AS build

ARG CMD
ARG VERSION=dev

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X fleetpulse/internal/version.Version=${VERSION}" \
    -o /out/app ./cmd/${CMD}

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/app /app

ENTRYPOINT ["/app"]
