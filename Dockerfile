FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/unas-protect-exporter ./cmd/unas-protect-exporter

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/unas-protect-exporter /unas-protect-exporter
EXPOSE 9101
USER nonroot:nonroot
ENTRYPOINT ["/unas-protect-exporter"]
