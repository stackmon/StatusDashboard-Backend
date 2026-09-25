# syntax=docker/dockerfile:1

# Create a stage for building the application.
ARG GO_VERSION=1.26
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
WORKDIR /usr/src/app

# Cache dependencies
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=bind,source=go.sum,target=go.sum \
    --mount=type=bind,source=go.mod,target=go.mod \
    go mod download -x

ARG TARGETARCH

# Build the application
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=bind,target=. \
    CGO_ENABLED=0 GOARCH=$TARGETARCH go build -o /usr/local/bin/app cmd/main.go

# Stage 2: Create a minimal production image
FROM alpine:3.24 AS final

# Install necessary packages
RUN --mount=type=cache,target=/var/cache/apk \
    apk --update add \
        ca-certificates \
        tzdata \
        make \
        && \
        update-ca-certificates

# Create a non-privileged user
ARG UID=1001
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/nonexistent" \
    --shell "/sbin/nologin" \
    --no-create-home \
    --uid "${UID}" \
    appuser

USER appuser

WORKDIR /usr/src/app

# Copy the binary from the build stage and the OpenAPI spec from the build context.
COPY --from=build --chown=appuser:appuser /usr/local/bin/app .
COPY --chown=appuser:appuser openapi.yaml .

# Expose the port
EXPOSE 8000

# Define the command to run
ENTRYPOINT [ "/usr/src/app/app" ]
