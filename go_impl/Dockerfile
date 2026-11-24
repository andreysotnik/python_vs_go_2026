# ---- build stage (also used by the compose "test" profile) ----
FROM golang:1.26-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-mod=vendor GOTOOLCHAIN=local
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/library ./cmd/library

# ---- runtime: distroless static, non-root, ~15 MB ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/library /library
USER nonroot:nonroot
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --retries=3 CMD ["/library", "healthcheck"]
ENTRYPOINT ["/library"]
CMD ["serve"]
