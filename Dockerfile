# Play with Agents — one image, several modes (web | worker | migrate | mailcheck).
#
# Build stages run on the BUILD platform and cross-compile, so building the
# linux/arm64 image for the Graviton node from an amd64 laptop needs no
# emulation: nothing in the final stage executes at build time.
#
#   docker buildx build --platform linux/arm64 -t play:dev --load .

FROM --platform=$BUILDPLATFORM node:20-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN mkdir -p ../internal/web && npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS go
ARG TARGETOS=linux
ARG TARGETARCH=arm64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/play ./cmd/play

# distroless/static: CA certificates and tzdata, no shell, runs as nonroot.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/play /play
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/play"]
CMD ["web"]
