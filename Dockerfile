# --platform=$BUILDPLATFORM pins the build stage to the BUILDER's architecture
# so the Go toolchain runs natively and cross-compiles, rather than the whole
# stage running under QEMU. Without it a multi-arch build emulates the foreign
# architecture for the entire compile, which is slow; and if GOOS/GOARCH are not
# then wired from TARGETOS/TARGETARCH the binary comes out for the WRONG
# architecture, which fails only at container start with "exec format error".
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/unas-protect-exporter ./cmd/unas-protect-exporter

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/unas-protect-exporter /unas-protect-exporter
EXPOSE 9101
USER nonroot:nonroot
ENTRYPOINT ["/unas-protect-exporter"]
