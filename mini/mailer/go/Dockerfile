# ---- build stage ----
FROM golang:1.26-alpine AS build
WORKDIR /src
# Dependencies are vendored: the build needs no network access.
ENV CGO_ENABLED=0 GOFLAGS=-mod=vendor GOTOOLCHAIN=local
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY cmd ./cmd
COPY internal ./internal
RUN go build -trimpath -ldflags="-s -w" -o /out/mailer ./cmd/mailer \
 && mkdir -p /out/state

# ---- runtime stage: distroless, non-root (uid 65532), CA certs included ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mailer /usr/local/bin/mailer
COPY --from=build --chown=65532:65532 /out/state /var/lib/mailer
COPY --chown=65532:65532 examples /etc/mailer
ENV STATE_DIR=/var/lib/mailer
USER 65532:65532
VOLUME ["/var/lib/mailer"]
ENTRYPOINT ["/usr/local/bin/mailer"]
CMD ["run", "--config", "/etc/mailer/jobs.yaml"]
